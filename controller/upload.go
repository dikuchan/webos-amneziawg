package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
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
const uploadStatePath = base + "/run/upload-state.json"
const uploadPort = 8089

type uploadOptions struct {
	Host     string
	Code     string
	Language string
	Expires  time.Time
	Now      func() time.Time
}
type uploadState struct {
	BootID   string      `json:"bootId"`
	Host     string      `json:"host"`
	Code     string      `json:"code"`
	Language string      `json:"language"`
	Expires  time.Time   `json:"expires"`
	Phase    uploadPhase `json:"phase"`
}
type uploadHandler struct {
	options  uploadOptions
	commit   func([]byte, candidateConfig) error
	complete func(uploadPhase)
	mu       sync.Mutex
	phase    uploadPhase
	failed   int
}

func newUploadHandler(options uploadOptions, commit func([]byte, candidateConfig) error, complete func(uploadPhase)) *uploadHandler {
	return &uploadHandler{options: options, commit: commit, complete: complete, phase: uploadWaiting}
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
func uploadText(lang, key string) string {
	messages := map[string][2]string{
		"title": {"Import configuration", "Импорт конфигурации"}, "code": {"Code from the TV", "Код с экрана ТВ"}, "file": {"Configuration (.conf)", "Конфигурация (.conf)"}, "submit": {"Import", "Импортировать"},
		"done": {"Configuration imported. You can connect on the TV.", "Конфигурация загружена. Можно подключаться на ТВ."}, "invalid": {"Invalid configuration file.", "Некорректный файл конфигурации."}, "expired": {"This upload session has ended. Reopen import on the TV.", "Загрузка завершена. Открой импорт на ТВ заново."}, "denied": {"Incorrect code.", "Неверный код."}, "busy": {"Disconnect the VPN on the TV and try again.", "Отключи VPN на ТВ и повтори загрузку."}, "error": {"Could not import the configuration. Check the TV logs.", "Не удалось импортировать конфигурацию. Проверь логи на ТВ."}, "back": {"Back", "Назад"},
	}
	values, ok := messages[key]
	if !ok {
		return key
	}
	if lang == "ru" {
		return values[1]
	}
	return values[0]
}

var uploadPage = template.Must(template.New("upload").Parse(`<!doctype html><html lang="{{.Lang}}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>AmneziaWG</title><style>
*{box-sizing:border-box}body{margin:0;min-height:100vh;background:#121113;color:#eeeef0;font:17px/1.5 Inter,system-ui,sans-serif;display:grid;place-items:center;padding:28px}main{width:100%;max-width:460px}header{font-size:18px;font-weight:600;margin-bottom:44px}h1{font-size:28px;line-height:1.25;font-weight:500;margin:0 0 32px}label{display:block;margin:24px 0 8px;color:#b5b2bc}input{display:block;width:100%;padding:15px;border:1px solid #3c393f;border-radius:12px;background:#232225;color:#eeeef0;font:inherit}input:focus,button:focus,a:focus{outline:3px solid #b1a9ff;outline-offset:3px}button{width:100%;margin-top:30px;padding:16px;border:0;border-radius:12px;background:#5b5bd6;color:white;font:inherit;cursor:pointer}.languages{display:flex;justify-content:flex-end;margin-bottom:24px}a{color:#b1a9ff;text-decoration:none;margin-left:20px}p{color:#b5b2bc}input[type=file]{font-size:15px}</style></head><body><main><div class="languages"><a href="/?lang=en">English</a><a href="/?lang=ru">Русский</a></div><header>AmneziaWG</header><h1>{{.Title}}</h1>{{if .Message}}<p role="status">{{.Message}}</p>{{if .Retry}}<a href="/?lang={{.Lang}}">{{.Back}}</a>{{end}}{{else}}<form action="/upload?lang={{.Lang}}" method="post" enctype="multipart/form-data"><label for="code">{{.CodeLabel}}</label><input id="code" name="code" inputmode="numeric" autocomplete="one-time-code" pattern="[0-9 ]{8,12}" maxlength="12" required><label for="config">{{.FileLabel}}</label><input id="config" name="config" type="file" accept=".conf,text/plain" required><button type="submit">{{.Submit}}</button></form>{{end}}</main></body></html>`))

func writeUploadPage(w http.ResponseWriter, lang string, code int, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	data := struct {
		Lang, Title, CodeLabel, FileLabel, Submit, Message, Back string
		Retry                                                    bool
	}{lang, uploadText(lang, "title"), uploadText(lang, "code"), uploadText(lang, "file"), uploadText(lang, "submit"), message, uploadText(lang, "back"), code >= 400 && code != http.StatusGone}
	if err := uploadPage.Execute(w, data); err != nil {
		log.Print("upload response could not be written")
	}
}
func (handler *uploadHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	lang := uploadLang(r.URL.Query().Get("lang"), handler.options.Language)
	if r.Host != handler.options.Host || r.Header.Get("Origin") != "" && r.Header.Get("Origin") != "http://"+handler.options.Host || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	handler.mu.Lock()
	defer handler.mu.Unlock()
	if handler.phase != uploadWaiting || !handler.options.Now().Before(handler.options.Expires) {
		if handler.phase == uploadWaiting {
			handler.phase = uploadExpired
			handler.complete(uploadExpired)
		}
		writeUploadPage(w, lang, http.StatusGone, uploadText(lang, "expired"))
		return
	}
	if r.URL.Path == "/" && r.Method == "GET" {
		writeUploadPage(w, lang, http.StatusOK, "")
		return
	}
	if r.URL.Path != "/upload" || r.Method != "POST" {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxConfigBytes+8192)
	if err := r.ParseMultipartForm(maxConfigBytes + 8192); err != nil {
		writeUploadPage(w, lang, http.StatusBadRequest, uploadText(lang, "invalid"))
		return
	}
	defer func() {
		if err := r.MultipartForm.RemoveAll(); err != nil {
			log.Print("upload temporary data cleanup failed")
		}
	}()
	code := strings.ReplaceAll(r.FormValue("code"), " ", "")
	if len(code) > 32 || subtle.ConstantTimeCompare([]byte(code), []byte(handler.options.Code)) != 1 {
		handler.failed++
		if handler.failed >= 5 {
			handler.phase = uploadLocked
			handler.complete(uploadLocked)
		}
		writeUploadPage(w, lang, http.StatusForbidden, uploadText(lang, "denied"))
		return
	}
	if len(r.MultipartForm.File) != 1 || len(r.MultipartForm.File["config"]) != 1 {
		writeUploadPage(w, lang, http.StatusBadRequest, uploadText(lang, "invalid"))
		return
	}
	file, header, err := r.FormFile("config")
	if err != nil {
		writeUploadPage(w, lang, http.StatusBadRequest, uploadText(lang, "invalid"))
		return
	}
	defer file.Close()
	if !strings.EqualFold(filepath.Ext(header.Filename), ".conf") {
		writeUploadPage(w, lang, http.StatusBadRequest, uploadText(lang, "invalid"))
		return
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxConfigBytes+1))
	if err != nil || len(raw) > maxConfigBytes {
		writeUploadPage(w, lang, http.StatusBadRequest, uploadText(lang, "invalid"))
		return
	}
	candidate, err := parseConfig(raw)
	if err != nil {
		writeUploadPage(w, lang, http.StatusBadRequest, uploadText(lang, "invalid"))
		return
	}
	if err := handler.commit(raw, candidate); err != nil {
		status, key := http.StatusInternalServerError, "error"
		if errors.Is(err, errImportActive) || errors.Is(err, errOperationBusy) {
			status, key = http.StatusConflict, "busy"
		}
		writeUploadPage(w, lang, status, uploadText(lang, key))
		return
	}
	handler.phase = uploadImported
	handler.complete(uploadImported)
	writeUploadPage(w, lang, http.StatusOK, uploadText(lang, "done"))
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
	if err := checkImportAllowed(); err != nil {
		return err
	}
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
	listener, err := net.Listen("tcp4", state.Host)
	if err != nil {
		state.Phase = uploadFailed
		state.Code = ""
		if saveErr := saveUploadState(state); saveErr != nil {
			return saveErr
		}
		return errors.New("LAN upload listener failed")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
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
	handler := newUploadHandler(uploadOptions{Host: state.Host, Code: state.Code, Language: state.Language, Expires: state.Expires, Now: time.Now}, func(raw []byte, candidate candidateConfig) error {
		return withLock(func() error {
			if err := suppressBoot(); err != nil {
				return err
			}
			if err := commitConfig(raw, candidate); err != nil {
				return err
			}
			recordEvent("Configuration imported over LAN.")
			return nil
		})
	}, finish)
	var connections atomic.Int32
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 5 * time.Second, MaxHeaderBytes: 4096, ErrorLog: log.New(io.Discard, "", 0), ConnState: func(conn net.Conn, phase http.ConnState) {
		switch phase {
		case http.StateNew:
			if connections.Add(1) > 4 {
				_ = conn.Close()
			}
		case http.StateClosed, http.StateHijacked:
			connections.Add(-1)
		}
	}}
	go func() {
		<-ctx.Done()
		shutdown, done := context.WithTimeout(context.Background(), 2*time.Second)
		defer done()
		if err := server.Shutdown(shutdown); err != nil {
			_ = server.Close()
		}
	}()
	state.Phase = uploadWaiting
	if err := saveUploadState(state); err != nil {
		_ = listener.Close()
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
