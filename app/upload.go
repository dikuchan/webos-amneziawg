package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

type uploadPhase string

const (
	uploadStarting uploadPhase = "starting"
	uploadWaiting  uploadPhase = "waiting"
	uploadImported uploadPhase = "imported"
	uploadClosed   uploadPhase = "closed"
	uploadExpired  uploadPhase = "expired"
	uploadLocked   uploadPhase = "locked"
	uploadFailed   uploadPhase = "failed"
)

const (
	uploadStatePath = base + "/run/lan-state.json"
	uploadPort      = 8089
)

type uploadOptions struct {
	Host, Code, Token, Language string
	Expires                     time.Time
	Now                         func() time.Time
}
type uploadState struct {
	BootID   string      `json:"bootId"`
	Host     string      `json:"host"`
	Code     string      `json:"code"`
	Language string      `json:"language"`
	Expires  time.Time   `json:"expires"`
	Phase    uploadPhase `json:"phase"`
}
type lanBackend struct {
	Load   func() (routingDocument, error)
	Save   func(context.Context, string, routingDocument) error
	Import func([]byte, candidateConfig) error
}
type uploadHandler struct {
	options  uploadOptions
	backend  lanBackend
	complete func(uploadPhase)
	mu       sync.Mutex
	phase    uploadPhase
	failed   int
	draft    []listSource
}

func newLANHandler(options uploadOptions, backend lanBackend, complete func(uploadPhase)) *uploadHandler {
	return &uploadHandler{options: options, backend: backend, complete: complete, phase: uploadWaiting}
}

func uploadLang(value, fallback string) string {
	if value == "ru" || value == "en" {
		return value
	}
	if fallback == "ru" {
		return "ru"
	}
	return "en"
}

func lanText(lang, key string) string {
	messages := map[string][2]string{
		"connect":    {"Connect to the TV", "Подключение к ТВ"},
		"code":       {"Code from the TV", "Код с экрана ТВ"},
		"open":       {"Open", "Открыть"},
		"profile":    {"VPN profile", "Профиль VPN"},
		"routing":    {"Routing", "Маршрутизация"},
		"configFile": {"Configuration (.conf)", "Конфигурация (.conf)"},
		"import":     {"Import", "Импортировать"},
		"all":        {"All traffic through VPN", "Весь трафик через VPN"},
		"exclude":    {"Exclude lists from VPN", "Адреса из списков — напрямую"},
		"include":    {"Only lists through VPN", "Через VPN — только адреса из списков"},
		"format":     {"IPv4 addresses or CIDRs, one per line. Comments start with #.", "IPv4-адрес или подсеть в каждой строке. Комментарии начинаются с #."},
		"add":        {"Add source", "Добавить источник"},
		"save":       {"Save", "Сохранить"},
		"name":       {"Name", "Название"},
		"remove":     {"Remove", "Удалить"},
		"type":       {"Source", "Источник"},
		"file":       {"Local TXT file", "Локальный TXT-файл"},
		"textFile":   {"IPv4 TXT file", "TXT-файл с IPv4"},
		"checksum":   {"SHA-256 URL (optional)", "URL контрольной суммы SHA-256 (необязательно)"},
		"interval":   {"Update interval, hours (1–168)", "Интервал обновления, ч (1–168)"},
		"subnets":    {"subnets", "подсетей"},
		"cached":     {"Saved list; last update failed", "Сохранённый список; обновление не удалось"},
		"temporary":  {"This page is available while web settings are open on the TV, for up to 10 minutes.", "Страница доступна, пока веб-настройки открыты на ТВ, максимум 10 минут."},
		"saved":      {"Routing settings saved.", "Настройки маршрутизации сохранены."},
		"done":       {"Configuration imported. You can connect on the TV.", "Конфигурация загружена. Можно подключаться на ТВ."},
		"invalid":    {"Check the file and source settings. IPv4 TXT only; at most 8 sources and 16384 subnets.", "Проверь файл и параметры источников. Поддерживается IPv4 TXT, до 8 источников и 16384 подсетей."},
		"denied":     {"Incorrect code.", "Неверный код."},
		"expired":    {"This session has ended. Reopen web settings on the TV.", "Сеанс завершён. Открой веб-настройки на ТВ заново."},
		"busy":       {"Disconnect the VPN before replacing its profile.", "Отключи VPN перед заменой профиля."},
		"failed":     {"Could not save. Check source URLs, connectivity and the TV logs.", "Не удалось сохранить. Проверь адреса источников, подключение и логи на ТВ."},
		"conflict":   {"Settings changed on the TV. Reload this page.", "Настройки изменились на ТВ. Обнови страницу."},
		"operation":  {"Another operation is running. Try again.", "Выполняется другая операция. Повтори попытку."},
	}
	values, ok := messages[key]
	if !ok {
		panic("missing LAN translation")
	}
	if lang == "ru" {
		return values[1]
	}
	return values[0]
}

//go:embed .build/lan/admin.html
var lanHTML string

//go:embed .build/lan/admin.js
var lanJS []byte

//go:embed .build/lan/admin.css
var lanCSS []byte
var lanPage = template.Must(template.New("lan").Parse(lanHTML))

type sourceRow struct {
	Index   string
	Spec    sourceSpec
	Cached  string
	Count   int
	Checked string
	State   sourceState
	T       func(string) string
}
type lanView struct {
	Lang, Page, Path, Message, MessageClass, Revision string
	Authenticated                                     bool
	Mode                                              routingMode
	Sources                                           []sourceRow
	Blank                                             sourceRow
	T                                                 func(string) string
}

func (h *uploadHandler) render(w http.ResponseWriter, lang, page string, status int, key string, d *routingDocument, revision string) {
	t := func(key string) string { return lanText(lang, key) }
	view := lanView{Lang: lang, Page: page, Path: "/", Authenticated: page != "login" && page != "ended", T: t, Blank: sourceRow{Index: "__index__", Spec: sourceSpec{Kind: sourceRemote, EveryHours: 24}, T: t}}
	if page == "routing" {
		view.Path = "/routing"
	}
	if key != "" {
		view.Message = t(key)
		if status >= 400 {
			view.MessageClass = "error"
		}
	}
	if d != nil {
		view.Mode = d.Mode
		view.Revision = revision
		if view.Revision == "" {
			view.Revision = policyRevision(*d)
		}
		for i, s := range d.Sources {
			p, err := parseIPv4Text(s.Cache.Raw)
			if err != nil {
				p = nil
			}
			row := sourceRow{Index: strconv.Itoa(i), Spec: s.Spec, State: s.Cache.State, Count: len(p), T: t}
			if validCache(s.Cache) {
				row.Cached = "yes"
			}
			if !s.Cache.Checked.IsZero() {
				row.Checked = s.Cache.Checked.UTC().Format(time.RFC3339)
			}
			view.Sources = append(view.Sources, row)
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := lanPage.Execute(w, view); err != nil {
		log.Print("LAN response could not be written")
	}
}

func (h *uploadHandler) authenticated(r *http.Request) bool {
	cookie, err := r.Cookie("amneziawg-lan")
	return err == nil && len(cookie.Value) == 64 && len(h.options.Token) == 64 && subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(h.options.Token)) == 1
}

func (h *uploadHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	lang := uploadLang(r.URL.Query().Get("lang"), h.options.Language)
	if r.Host != h.options.Host || r.Header.Get("Origin") != "" && r.Header.Get("Origin") != "http://"+h.options.Host || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.phase != uploadWaiting || !h.options.Now().Before(h.options.Expires) {
		if h.phase == uploadWaiting {
			h.phase = uploadExpired
			h.complete(uploadExpired)
		}
		h.render(w, lang, "ended", http.StatusGone, "expired", nil, "")
		return
	}
	if r.Method == "GET" && r.URL.Path == "/admin.css" {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		if _, err := w.Write(lanCSS); err != nil {
			log.Print("LAN stylesheet response failed")
		}
		return
	}
	if r.Method == "GET" && r.URL.Path == "/editor.js" {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		if _, err := w.Write(lanJS); err != nil {
			log.Print("LAN script response failed")
		}
		return
	}
	if r.Method == "POST" && r.URL.Path == "/login" {
		r.Body = http.MaxBytesReader(w, r.Body, 1024)
		if err := r.ParseForm(); err != nil {
			h.render(w, lang, "login", 400, "denied", nil, "")
			return
		}
		code := strings.ReplaceAll(r.PostForm.Get("code"), " ", "")
		if subtle.ConstantTimeCompare([]byte(code), []byte(h.options.Code)) != 1 {
			h.failed++
			if h.failed >= 5 {
				h.phase = uploadLocked
				h.complete(uploadLocked)
			}
			h.render(w, lang, "login", 403, "denied", nil, "")
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "amneziawg-lan", Value: h.options.Token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 600})
		http.Redirect(w, r, "/?lang="+lang, http.StatusSeeOther)
		return
	}
	if !h.authenticated(r) {
		status := http.StatusOK
		if r.Method != "GET" {
			status = http.StatusForbidden
		}
		h.render(w, lang, "login", status, "", nil, "")
		return
	}
	if r.Method == "GET" && r.URL.Path == "/" {
		h.render(w, lang, "profile", 200, "", nil, "")
		return
	}
	if r.Method == "GET" && r.URL.Path == "/routing" {
		document, err := h.backend.Load()
		if err != nil {
			h.render(w, lang, "routing", 500, "failed", nil, "")
			return
		}
		h.render(w, lang, "routing", 200, "", &document, "")
		return
	}
	if r.Method != "POST" || (r.URL.Path != "/routing" && r.URL.Path != "/upload") {
		http.Error(w, "Not found", 404)
		return
	}
	limit := int64(sourceTotalByteLimit + 65536)
	if r.URL.Path == "/upload" {
		limit = maxConfigBytes + 8192
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	if err := r.ParseMultipartForm(limit); err != nil {
		if r.URL.Path == "/routing" {
			current, loadErr := h.backend.Load()
			if loadErr != nil {
				h.render(w, lang, "routing", 500, "failed", nil, "")
				return
			}
			h.render(w, lang, "routing", 400, "invalid", &current, "")
		} else {
			h.render(w, lang, "profile", 400, "invalid", nil, "")
		}
		return
	}
	defer func() {
		if err := r.MultipartForm.RemoveAll(); err != nil {
			log.Print("LAN temporary upload cleanup failed")
		}
	}()
	if r.URL.Path == "/upload" {
		h.importProfile(w, r, lang)
		return
	}
	current, err := h.backend.Load()
	if err != nil {
		h.render(w, lang, "routing", 500, "failed", nil, "")
		return
	}
	document, err := h.parseRoutingForm(r, current)
	if err != nil {
		h.render(w, lang, "routing", 400, "invalid", &document, policyRevision(current))
		return
	}
	expected := r.FormValue("revision")
	if expected != policyRevision(current) {
		h.render(w, lang, "routing", 409, "conflict", &current, "")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	err = h.backend.Save(ctx, expected, document)
	if err != nil {
		recordEvent("Routing settings could not be saved: " + err.Error())
		status, key := http.StatusBadGateway, "failed"
		if errors.Is(err, errRoutingConflict) {
			status, key = 409, "conflict"
		}
		if errors.Is(err, errOperationBusy) {
			status, key = 409, "operation"
		}
		h.render(w, lang, "routing", status, key, &document, policyRevision(current))
		return
	}
	h.draft = nil
	document, err = h.backend.Load()
	if err != nil {
		h.render(w, lang, "routing", 500, "failed", nil, "")
		return
	}
	h.render(w, lang, "routing", 200, "saved", &document, "")
}

func (h *uploadHandler) importProfile(w http.ResponseWriter, r *http.Request, lang string) {
	if len(r.MultipartForm.File) != 1 || len(r.MultipartForm.File["config"]) != 1 {
		h.render(w, lang, "profile", 400, "invalid", nil, "")
		return
	}
	file, header, err := r.FormFile("config")
	if err != nil {
		h.render(w, lang, "profile", 400, "invalid", nil, "")
		return
	}
	defer closeResource(file)
	if !strings.EqualFold(filepath.Ext(header.Filename), ".conf") {
		h.render(w, lang, "profile", 400, "invalid", nil, "")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxConfigBytes+1))
	if err != nil || len(raw) > maxConfigBytes {
		h.render(w, lang, "profile", 400, "invalid", nil, "")
		return
	}
	candidate, err := parseConfig(raw)
	if err != nil {
		h.render(w, lang, "profile", 400, "invalid", nil, "")
		return
	}
	if err := h.backend.Import(raw, candidate); err != nil {
		status, key := 500, "failed"
		if errors.Is(err, errImportActive) {
			status, key = 409, "busy"
		}
		if errors.Is(err, errOperationBusy) {
			status, key = 409, "operation"
		}
		h.render(w, lang, "profile", status, key, nil, "")
		return
	}
	h.phase = uploadImported
	h.complete(uploadImported)
	h.render(w, lang, "ended", 200, "done", nil, "")
}

func (h *uploadHandler) parseRoutingForm(r *http.Request, current routingDocument) (routingDocument, error) {
	document := routingDocument{Version: 1, Mode: routingMode(r.FormValue("mode"))}
	var invalid error
	seen := make(map[sourceID]struct{})
	presets := 0
	total := 0
	for key := range r.MultipartForm.Value {
		if strings.HasPrefix(key, "kind-") {
			index, err := strconv.Atoi(strings.TrimPrefix(key, "kind-"))
			if err != nil || index < 0 || index >= sourceLimit {
				return document, errors.New("too many sources")
			}
		}
	}
	previous := append(append([]listSource(nil), current.Sources...), h.draft...)
	for i := 0; i < sourceLimit; i++ {
		suffix := strconv.Itoa(i)
		kind := sourceKind(r.FormValue("kind-" + suffix))
		if kind == "" {
			continue
		}
		id := sourceID(r.FormValue("id-" + suffix))
		if id == "" {
			var err error
			id, err = newSourceID()
			if err != nil {
				return document, err
			}
		}
		spec := sourceSpec{ID: id, Kind: kind, Name: strings.TrimSpace(r.FormValue("name-" + suffix)), URL: strings.TrimSpace(r.FormValue("url-" + suffix)), ChecksumURL: strings.TrimSpace(r.FormValue("checksum-" + suffix))}
		if kind != sourceLocal {
			hours, err := strconv.ParseUint(r.FormValue("hours-"+suffix), 10, 16)
			if err != nil {
				invalid = errors.New("invalid update interval")
			}
			spec.EveryHours = uint16(hours)
		}
		source := listSource{Spec: spec, Cache: sourceCache{State: sourceMissing}}
		for _, old := range previous {
			if old.Spec.ID == id && old.Spec.Kind == kind && old.Spec.URL == spec.URL && old.Spec.ChecksumURL == spec.ChecksumURL {
				source.Cache = old.Cache
				break
			}
		}
		if kind == sourceRU {
			presets++
			if !validCache(source.Cache) {
				d, err := bundledDataset()
				if err != nil {
					return document, err
				}
				source.Cache = sourceCache{Raw: d.Source, Hash: d.Hash, Checked: d.Generated, State: sourceReady}
			}
		}
		if files := r.MultipartForm.File["file-"+suffix]; len(files) > 0 {
			if kind != sourceLocal || len(files) != 1 {
				return document, errors.New("invalid source file")
			}
			file, err := files[0].Open()
			if err != nil {
				return document, errors.New("cannot read list")
			}
			raw, readErr := io.ReadAll(io.LimitReader(file, sourceByteLimit+1))
			closeErr := file.Close()
			if readErr != nil || closeErr != nil {
				return document, errors.New("cannot read list")
			}
			if _, err := parseIPv4Text(raw); err != nil {
				invalid = err
			} else {
				source.Cache = sourceCache{Raw: raw, Hash: contentHash(raw), Checked: h.options.Now(), State: sourceReady}
			}
		}
		if _, ok := seen[id]; ok {
			invalid = errors.New("duplicate source")
		}
		seen[id] = struct{}{}
		if err := validateSourceSpec(spec); err != nil {
			invalid = err
		}
		if kind == sourceLocal && !validCache(source.Cache) {
			invalid = errListsMissing
		}
		total += len(source.Cache.Raw)
		document.Sources = append(document.Sources, source)
	}
	if !validMode(document.Mode) || presets > 1 || total > sourceTotalByteLimit || document.Mode != modeAll && len(document.Sources) == 0 {
		invalid = errors.New("invalid routing settings")
	}
	h.draft = document.Sources
	return document, invalid
}

func readUploadState() (*uploadState, error) {
	raw, err := readRegular(uploadStatePath, 4096)
	if err != nil || len(raw) == 0 {
		return nil, err
	}
	var state uploadState
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, errors.New("invalid upload session state")
	}
	id, err := bootID()
	if err != nil {
		return nil, err
	}
	if state.BootID != id {
		return nil, nil
	}
	host, port, err := net.SplitHostPort(state.Host)
	address, addressErr := netip.ParseAddr(host)
	live := state.Phase == uploadStarting || state.Phase == uploadWaiting
	validPhase := live || state.Phase == uploadImported || state.Phase == uploadClosed || state.Phase == uploadExpired || state.Phase == uploadLocked || state.Phase == uploadFailed
	validCode := len(state.Code) == 8
	for _, digit := range state.Code {
		if digit < '0' || digit > '9' {
			validCode = false
		}
	}
	if err != nil || addressErr != nil || !address.Is4() || !address.IsPrivate() || port != "8089" || state.Expires.IsZero() || !validPhase || state.Language != "en" && state.Language != "ru" || live && !validCode || !live && state.Code != "" {
		return nil, errors.New("invalid upload session state")
	}
	return &state, nil
}

func saveUploadState(state *uploadState) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return atomicWrite(uploadStatePath, raw)
}

func physicalLANAddress(ctx context.Context) (string, error) {
	route, err := command(ctx, "ip", "-4", "route", "show", "default")
	if err != nil {
		return "", err
	}
	dev := fieldAfter(route, "dev")
	if dev == "" || dev == iface || dev == "wg0" {
		return "", errors.New("physical LAN interface is unavailable")
	}
	network, err := net.InterfaceByName(dev)
	if err != nil {
		return "", err
	}
	addresses, err := network.Addrs()
	if err != nil {
		return "", err
	}
	for _, value := range addresses {
		prefix, err := netip.ParsePrefix(value.String())
		if err == nil && prefix.Addr().Is4() && prefix.Addr().IsPrivate() {
			return prefix.Addr().String(), nil
		}
	}
	return "", errors.New("private LAN IPv4 address is unavailable")
}

func openUpload(ctx context.Context, kit, language string) error {
	if err := suppressBoot(); err != nil {
		return err
	}
	old, err := readUploadState()
	if err != nil {
		return err
	}
	worker, err := readWorker(uploadWorker)
	if err != nil {
		return err
	}
	if old != nil && old.Phase == uploadWaiting && time.Now().Before(old.Expires) && ownsWorker(worker) {
		return printUploadStatus()
	}
	if err := stopWorker(uploadWorker); err != nil {
		return err
	}
	address, err := physicalLANAddress(ctx)
	if err != nil {
		return err
	}
	code, err := rand.Int(rand.Reader, big.NewInt(100000000))
	if err != nil {
		return err
	}
	id, err := bootID()
	if err != nil {
		return err
	}
	state := &uploadState{BootID: id, Host: net.JoinHostPort(address, fmt.Sprint(uploadPort)), Code: fmt.Sprintf("%08d", code.Int64()), Language: uploadLang(language, "en"), Expires: time.Now().Add(10 * time.Minute), Phase: uploadStarting}
	if err := saveUploadState(state); err != nil {
		return err
	}
	if err := startWorker(kit, uploadWorker); err != nil {
		return err
	}
	for attempt := 0; attempt < 50; attempt++ {
		state, err = readUploadState()
		if err != nil {
			return err
		}
		if state != nil && state.Phase == uploadWaiting {
			recordEvent("LAN import opened.")
			return printUploadStatus()
		}
		if state != nil && state.Phase == uploadFailed {
			return errors.New("cannot listen on the LAN upload port")
		}
		time.Sleep(50 * time.Millisecond)
	}
	return errors.New("LAN upload server did not become ready")
}

func printUploadStatus() error {
	state, err := readUploadState()
	if err != nil {
		return err
	}
	if state == nil {
		fmt.Println("upload=closed")
		return nil
	}
	if state.Phase == uploadWaiting {
		worker, err := readWorker(uploadWorker)
		if err != nil {
			return err
		}
		if !ownsWorker(worker) || !time.Now().Before(state.Expires) {
			state.Phase = uploadExpired
			state.Code = ""
			if err := saveUploadState(state); err != nil {
				return err
			}
		}
	}
	fmt.Printf("upload=%s\n", state.Phase)
	if state.Phase == uploadWaiting {
		fmt.Printf("upload_url=http://%s\nupload_code=%s\nupload_expires=%d\n", state.Host, state.Code, state.Expires.Unix())
	}
	return nil
}

func closeUpload() error {
	if err := stopWorker(uploadWorker); err != nil {
		return err
	}
	cleanup, done := context.WithTimeout(context.Background(), 10*time.Second)
	defer done()
	if err := clearOrphanDownloadRoutes(cleanup); err != nil {
		return err
	}
	state, err := readUploadState()
	if err != nil {
		return err
	}
	if state != nil && state.Phase != uploadImported {
		state.Phase = uploadClosed
		state.Code = ""
		if err := saveUploadState(state); err != nil {
			return err
		}
	}
	return nil
}

func serveUpload() error {
	if err := prepareRuntime(); err != nil {
		return err
	}
	if err := waitForWorkerIdentity(uploadWorker); err != nil {
		return err
	}
	state, err := readUploadState()
	if err != nil || state == nil {
		return errors.New("upload session is unavailable")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp4", state.Host)
	if err != nil {
		state.Phase = uploadFailed
		state.Code = ""
		if saveErr := saveUploadState(state); saveErr != nil {
			return saveErr
		}
		return errors.New("LAN upload listener failed")
	}
	defer closeResource(listener)
	lifetime := time.Until(state.Expires)
	if lifetime > 10*time.Minute {
		lifetime = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, lifetime)
	defer cancel()
	var stateMu sync.Mutex
	finish := func(phase uploadPhase) {
		stateMu.Lock()
		defer stateMu.Unlock()
		if state.Phase == uploadImported {
			return
		}
		state.Phase = phase
		state.Code = ""
		if err := saveUploadState(state); err != nil {
			recordEvent("LAN import session state could not be saved.")
		}
		cancel()
	}
	var token [32]byte
	if _, err := rand.Read(token[:]); err != nil {
		return err
	}
	handler := newLANHandler(uploadOptions{Host: state.Host, Code: state.Code, Token: hex.EncodeToString(token[:]), Language: state.Language, Expires: state.Expires, Now: time.Now}, lanBackend{
		Load: readRoutingDocument, Save: saveEditorRouting,
		Import: func(raw []byte, candidate candidateConfig) error {
			return withLock(func() error {
				if err := suppressBoot(); err != nil {
					return err
				}
				if err := commitConfig(raw, candidate); err != nil {
					return err
				}
				recordEvent("VPN profile imported over LAN.")
				return nil
			})
		},
	}, finish)
	var connections atomic.Int32
	server := &http.Server{Handler: handler, BaseContext: func(net.Listener) context.Context { return ctx }, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 75 * time.Second, IdleTimeout: 5 * time.Second, MaxHeaderBytes: 4096, ErrorLog: log.New(io.Discard, "", 0), ConnState: func(conn net.Conn, phase http.ConnState) {
		switch phase {
		case http.StateNew:
			if connections.Add(1) > 4 {
				closeResource(conn)
			}
		case http.StateActive, http.StateIdle:
			// These transitions do not change the connection count.
		case http.StateClosed, http.StateHijacked:
			connections.Add(-1)
		}
	}}
	go func() {
		<-ctx.Done()
		shutdown, done := context.WithTimeout(context.Background(), 2*time.Second)
		defer done()
		if err := server.Shutdown(shutdown); err != nil {
			closeResource(server)
		}
	}()
	state.Phase = uploadWaiting
	if err := saveUploadState(state); err != nil {
		closeResource(listener)
		return err
	}
	err = server.Serve(listener)
	stateMu.Lock()
	if state.Phase == uploadWaiting {
		state.Phase = uploadClosed
		if !time.Now().Before(state.Expires) {
			state.Phase = uploadExpired
		}
		state.Code = ""
		if saveErr := saveUploadState(state); saveErr != nil {
			stateMu.Unlock()
			return saveErr
		}
	}
	stateMu.Unlock()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
