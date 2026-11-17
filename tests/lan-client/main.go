// Exercise the public LAN editor using synthetic files in an isolated container.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type source struct {
	ID, Kind, Name, URL, Checksum, Text string
	Hours                               int
}
type policy struct {
	Mode    string
	Sources []source
}

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if len(os.Args) != 2 {
		return fmt.Errorf("expected synthetic policy JSON file")
	}
	var state struct{ Host, Code string }
	raw, err := os.ReadFile("/var/lib/webosbrew/amneziawg/run/lan-state.json")
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return err
	}
	var desired policy
	raw, err = os.ReadFile(os.Args[1])
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, &desired); err != nil {
		return err
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return err
	}
	client := &http.Client{
		Jar:     jar,
		Timeout: 70 * time.Second,
	}
	origin := "http://" + state.Host
	login, err := http.NewRequestWithContext(ctx, http.MethodPost, origin+"/login", strings.NewReader(url.Values{
		"code": {
			state.Code,
		},
	}.Encode()))
	if err != nil {
		return err
	}
	login.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := client.Do(login)
	if err != nil {
		return err
	}
	if err := response.Body.Close(); err != nil {
		return err
	}
	listing, err := http.NewRequestWithContext(ctx, http.MethodGet, origin+"/routing", nil)
	if err != nil {
		return err
	}
	response, err = client.Do(listing)
	if err != nil {
		return err
	}
	page, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	closeErr := response.Body.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	match := regexp.MustCompile(`name="revision" value="([a-f0-9]{64})"`).FindSubmatch(page)
	if response.StatusCode != 200 || len(match) != 2 {
		return fmt.Errorf("editor did not return an authenticated revision")
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	fields := map[string]string{
		"mode":     desired.Mode,
		"revision": string(match[1]),
	}
	for i, s := range desired.Sources {
		n := strconv.Itoa(i)
		fields["id-"+n] = s.ID
		fields["kind-"+n] = s.Kind
		fields["name-"+n] = s.Name
		fields["url-"+n] = s.URL
		fields["checksum-"+n] = s.Checksum
		fields["hours-"+n] = strconv.Itoa(s.Hours)
		if s.Kind == "file" && s.Text != "" {
			part, err := writer.CreateFormFile("file-"+n, "synthetic.txt")
			if err != nil {
				return err
			}
			if _, err := io.WriteString(part, s.Text); err != nil {
				return err
			}
		}
	}
	for name, value := range fields {
		if err := writer.WriteField(name, value); err != nil {
			return err
		}
	}
	if err := writer.Close(); err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, "POST", origin+"/routing", &body)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Origin", origin)
	response, err = client.Do(request)
	if err != nil {
		return err
	}
	_, readErr := io.Copy(io.Discard, io.LimitReader(response.Body, 2<<20))
	closeErr = response.Body.Close()
	if readErr != nil {
		return readErr
	}
	if closeErr != nil {
		return closeErr
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("routing save returned HTTP %d", response.StatusCode)
	}
	fmt.Println("PASS: authenticated LAN policy saved")
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
