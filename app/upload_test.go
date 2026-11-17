package main

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

func testUploadOptions() uploadOptions {
	return uploadOptions{
		Host:     "192.168.0.64:8089",
		Code:     "12345678",
		Token:    strings.Repeat("a", 64),
		Language: "en",
		Expires:  time.Date(2026, 11, 17, 1, 10, 0, 0, time.UTC),
		Now:      func() time.Time { return time.Date(2026, 11, 17, 1, 0, 0, 0, time.UTC) },
	}
}

func testLANBackend(t *testing.T, commit func([]byte, candidateConfig) error) lanBackend {
	t.Helper()
	return lanBackend{
		Load:   defaultRoutingDocument,
		Save:   func(context.Context, string, routingDocument) error { return nil },
		Import: commit,
	}
}

func loginRequest(code string) *http.Request {
	request := httptest.NewRequestWithContext(context.Background(), "POST", "http://192.168.0.64:8089/login", strings.NewReader(url.Values{
		"code": []string{
			code,
		},
	}.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Origin", "http://192.168.0.64:8089")
	return request
}

func loginLAN(t *testing.T, handler http.Handler) *http.Cookie {
	t.Helper()
	reply := httptest.NewRecorder()
	handler.ServeHTTP(reply, loginRequest("12345678"))
	cookies := reply.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal("no authenticated session")
	}
	return cookies[0]
}

func profileRequest(t *testing.T, raw string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("config", "provider.conf")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte(raw)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequestWithContext(context.Background(), "POST", "http://192.168.0.64:8089/upload", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Origin", "http://192.168.0.64:8089")
	return request
}

func TestLANProfileImportRequiresLoginAndDoesNotEchoSecrets(t *testing.T) {
	commits := 0
	handler := newLANHandler(testUploadOptions(), testLANBackend(t, func(raw []byte, _ candidateConfig) error {
		commits++
		if string(raw) != exampleConfig() {
			t.Fatal("profile changed")
		}
		return nil
	}), func(uploadPhase) {})
	unauth := httptest.NewRecorder()
	handler.ServeHTTP(unauth, profileRequest(t, exampleConfig()))
	// no-referrer makes browsers send Origin: null for this POST form.
	if unauth.Header().Get("Referrer-Policy") != "same-origin" {
		t.Fatal("form origin policy would reject a legitimate browser submission")
	}
	if commits != 0 || unauth.Code < 400 {
		t.Fatal("unauthenticated import")
	}
	cookie := loginLAN(t, handler)
	request := profileRequest(t, exampleConfig())
	request.AddCookie(cookie)

	reply := httptest.NewRecorder()
	handler.ServeHTTP(reply, request)

	if reply.Code != 200 || commits != 1 {
		t.Fatal(reply.Code, commits)
	}
	if strings.Contains(reply.Body.String(), "PrivateKey") {
		t.Fatal("profile echoed")
	}
	request = profileRequest(t, exampleConfig())
	request.AddCookie(cookie)
	again := httptest.NewRecorder()
	handler.ServeHTTP(again, request)
	if again.Code != http.StatusGone || commits != 1 {
		t.Fatal("profile session reusable")
	}
}

func TestLANSessionRejectsForeignOriginAndLocksAfterFiveBadCodes(t *testing.T) {
	handler := newLANHandler(testUploadOptions(), testLANBackend(t, func([]byte, candidateConfig) error { t.Fatal("unexpected import"); return nil }), func(uploadPhase) {})
	request := loginRequest("12345678")
	request.Header.Set("Origin", "https://foreign.invalid")
	reply := httptest.NewRecorder()
	handler.ServeHTTP(reply, request)
	if reply.Code != 403 {
		t.Fatal("foreign origin allowed")
	}
	for i := 0; i < 5; i++ {
		handler.ServeHTTP(httptest.NewRecorder(), loginRequest("bad"))
	}
	reply = httptest.NewRecorder()
	handler.ServeHTTP(reply, loginRequest("12345678"))
	if reply.Code != http.StatusGone {
		t.Fatal("login lockout missing")
	}
}

func TestLANProfileValidationRunsBeforeMutation(t *testing.T) {
	for _, input := range []string{
		"private-invalid-input",
		strings.Repeat("x", maxConfigBytes+1),
	} {
		handler := newLANHandler(testUploadOptions(), testLANBackend(t, func([]byte, candidateConfig) error { t.Fatal("invalid profile imported"); return nil }), func(uploadPhase) {})
		cookie := loginLAN(t, handler)
		request := profileRequest(t, input)
		request.AddCookie(cookie)

		reply := httptest.NewRecorder()
		handler.ServeHTTP(reply, request)

		if reply.Code < 400 || strings.Contains(reply.Body.String(), "private-invalid-input") {
			t.Fatal("invalid input accepted or echoed")
		}
	}
}

func TestLANExpiredSessionCannotBeReused(t *testing.T) {
	options := testUploadOptions()
	options.Expires = options.Now().Add(-time.Second)
	handler := newLANHandler(options, testLANBackend(t, func([]byte, candidateConfig) error { t.Fatal("expired import"); return nil }), func(uploadPhase) {})
	reply := httptest.NewRecorder()
	handler.ServeHTTP(reply, loginRequest("12345678"))
	if reply.Code != http.StatusGone {
		t.Fatal("expired session accepted")
	}
}

func TestLANRoutingEditPreservesUploadedListsAcrossSettingsChanges(t *testing.T) {
	current := routingDocument{
		Version: 1,
		Mode:    modeAll,
	}
	backend := lanBackend{
		Load: func() (routingDocument, error) { return current, nil },
		Save: func(_ context.Context, revision string, next routingDocument) error {
			if revision != policyRevision(current) {
				return errRoutingConflict
			}
			if _, err := documentPrefixes(next); err != nil {
				return err
			}
			current = next
			return nil
		},
	}
	handler := newLANHandler(testUploadOptions(), backend, func(uploadPhase) {})
	cookie := loginLAN(t, handler)
	post := func(mode routingMode, upload string) *httptest.ResponseRecorder {
		t.Helper()
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		for name, value := range map[string]string{
			"mode":     string(mode),
			"revision": policyRevision(current),
			"kind-0":   "file",
			"id-0":     "local-one",
			"name-0":   "Home list",
		} {
			if err := writer.WriteField(name, value); err != nil {
				t.Fatal(err)
			}
		}
		if upload != "" {
			part, err := writer.CreateFormFile("file-0", "list.txt")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := part.Write([]byte(upload)); err != nil {
				t.Fatal(err)
			}
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequestWithContext(context.Background(), "POST", "http://192.168.0.64:8089/routing", &body)
		request.Header.Set("Content-Type", writer.FormDataContentType())
		request.AddCookie(cookie)
		reply := httptest.NewRecorder()
		handler.ServeHTTP(reply, request)
		return reply
	}
	if reply := post(modeExclude, "5.45.192.1\n"); reply.Code != 200 {
		t.Fatal("initial list upload rejected", reply.Code)
	}

	reply := post(modeInclude, "")

	if reply.Code != 200 || current.Mode != modeInclude || len(current.Sources) != 1 || string(current.Sources[0].Cache.Raw) != "5.45.192.1\n" {
		t.Fatal("changing settings lost the uploaded list", reply.Code)
	}
}

func TestLANInvalidListCanBeCorrectedWithoutReloadingEditor(t *testing.T) {
	current := routingDocument{
		Version: 1,
		Mode:    modeAll,
	}
	backend := lanBackend{
		Load: func() (routingDocument, error) { return current, nil },
		Save: func(_ context.Context, revision string, next routingDocument) error {
			if revision != policyRevision(current) {
				return errRoutingConflict
			}
			current = next
			return nil
		},
	}
	handler := newLANHandler(testUploadOptions(), backend, func(uploadPhase) {})
	cookie := loginLAN(t, handler)
	post := func(revision, content string) *httptest.ResponseRecorder {
		t.Helper()
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		for name, value := range map[string]string{
			"mode":     "include",
			"revision": revision,
			"kind-0":   "file",
			"id-0":     "local-one",
		} {
			if err := writer.WriteField(name, value); err != nil {
				t.Fatal(err)
			}
		}
		part, err := writer.CreateFormFile("file-0", "list.txt")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequestWithContext(context.Background(), "POST", "http://192.168.0.64:8089/routing", &body)
		request.Header.Set("Content-Type", writer.FormDataContentType())
		request.AddCookie(cookie)
		reply := httptest.NewRecorder()
		handler.ServeHTTP(reply, request)
		return reply
	}
	invalid := post(policyRevision(current), "example.org\n")
	if invalid.Code != 400 {
		t.Fatal("domain list was accepted")
	}
	match := regexp.MustCompile(`name="revision" value="([a-f0-9]{64})"`).FindStringSubmatch(invalid.Body.String())
	if len(match) != 2 {
		t.Fatal("editor revision missing")
	}

	corrected := post(match[1], "203.0.113.1\n")

	if corrected.Code != 200 || current.Mode != modeInclude || len(current.Sources) != 1 {
		t.Fatal("corrected list could not be saved", corrected.Code)
	}
}
