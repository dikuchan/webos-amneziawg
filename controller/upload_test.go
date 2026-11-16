package main

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func uploadRequest(t *testing.T, code string, config string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("code", code); err != nil {
		t.Fatal(err)
	}
	file, err := writer.CreateFormFile("config", "redshield.conf")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = file.Write([]byte(config)); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", "http://192.168.0.64:8089/upload?lang=en", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Origin", "http://192.168.0.64:8089")
	return request
}

func testUploadOptions() uploadOptions {
	return uploadOptions{Host: "192.168.0.64:8089", Code: "12345678", Language: "en", Expires: time.Date(2026, 11, 16, 18, 10, 0, 0, time.UTC), Now: func() time.Time { return time.Date(2026, 11, 16, 18, 0, 0, 0, time.UTC) }}
}

func TestLANUploadImportsOnceWithoutEchoingSecrets(t *testing.T) {
	// Arrange: one-time session, valid AWG profile, application-owned commit boundary.
	commits := 0
	handler := newUploadHandler(testUploadOptions(), func(raw []byte, _ candidateConfig) error {
		commits++
		if string(raw) != exampleConfig() {
			t.Fatal("import changed bytes")
		}
		return nil
	}, func(uploadPhase) {})
	response := httptest.NewRecorder()
	// Act.
	handler.ServeHTTP(response, uploadRequest(t, "12345678", exampleConfig()))
	// Assert: one atomic commit and a response without configuration contents.
	if response.Code != http.StatusOK || commits != 1 {
		t.Fatal(response.Code, commits)
	}
	if strings.Contains(response.Body.String(), "PrivateKey") || strings.Contains(response.Body.String(), "HeaderProtectionKey") {
		t.Fatal("response exposed configuration")
	}
	again := httptest.NewRecorder()
	handler.ServeHTTP(again, uploadRequest(t, "12345678", exampleConfig()))
	if again.Code != http.StatusGone || commits != 1 {
		t.Fatal("one-time upload was reusable")
	}
}

func TestLANUploadRejectsBadCodeOriginHostAndConfigBeforeCommit(t *testing.T) {
	for _, scenario := range []string{"code", "origin", "host", "config", "oversize"} {
		t.Run(scenario, func(t *testing.T) {
			// Arrange.
			commits := 0
			handler := newUploadHandler(testUploadOptions(), func([]byte, candidateConfig) error { commits++; return nil }, func(uploadPhase) {})
			code, config := "12345678", exampleConfig()
			if scenario == "code" {
				code = "wrong"
			}
			if scenario == "config" {
				config = "secret-invalid-input"
			}
			if scenario == "oversize" {
				config = strings.Repeat("x", maxConfigBytes+1)
			}
			request := uploadRequest(t, code, config)
			if scenario == "origin" {
				request.Header.Set("Origin", "https://unrelated.example")
			}
			if scenario == "host" {
				request.Host = "unrelated.example"
			}
			// Act.
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			// Assert.
			if response.Code < 400 || commits != 0 {
				t.Fatal("invalid upload committed", response.Code)
			}
			if strings.Contains(response.Body.String(), "secret-invalid-input") {
				t.Fatal("error echoed input")
			}
		})
	}
}

func TestLANUploadExpiresAndLocksOutRepeatedWrongCodes(t *testing.T) {
	// Arrange: deterministic time, no sleeping.
	options := testUploadOptions()
	options.Expires = options.Now().Add(-time.Second)
	handler := newUploadHandler(options, func([]byte, candidateConfig) error { t.Fatal("expired session committed"); return nil }, func(uploadPhase) {})
	// Act / Assert.
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, uploadRequest(t, "12345678", exampleConfig()))
	if response.Code != http.StatusGone {
		t.Fatal("expired session accepted")
	}
	handler = newUploadHandler(testUploadOptions(), func([]byte, candidateConfig) error { t.Fatal("locked session committed"); return nil }, func(uploadPhase) {})
	for i := 0; i < 5; i++ {
		handler.ServeHTTP(httptest.NewRecorder(), uploadRequest(t, "wrong", exampleConfig()))
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, uploadRequest(t, "12345678", exampleConfig()))
	if response.Code != http.StatusGone {
		t.Fatal("guess limit not enforced")
	}
}
