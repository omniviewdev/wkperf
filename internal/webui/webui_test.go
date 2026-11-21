package webui

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

const (
	testHost      = "127.0.0.1:8123"
	testUploadMiB = 1
)

var tokenPattern = regexp.MustCompile(`name="token" value="([^"]+)"`)

func openUI(t *testing.T) (http.Handler, string) {
	t.Helper()
	h := New(testHost, testUploadMiB)
	r := httptest.NewRequest(http.MethodGet, "http://"+testHost+"/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	token := tokenPattern.FindStringSubmatch(w.Body.String())
	if w.Code != http.StatusOK || len(token) != 2 {
		t.Fatalf("GET status %d, token found: %v", w.Code, len(token) == 2)
	}
	for _, name := range []string{"baseline", "candidate"} {
		if !strings.Contains(w.Body.String(), `label for="`+name+`"`) {
			t.Fatalf("missing accessible label for %s", name)
		}
	}
	for key, value := range map[string]string{
		"Cache-Control": "no-store", "Content-Security-Policy": "default-src 'none'", "Referrer-Policy": "no-referrer",
	} {
		if !strings.Contains(w.Header().Get(key), value) {
			t.Errorf("%s = %q; want %q", key, w.Header().Get(key), value)
		}
	}
	return h, token[1]
}

func fixture(end float64) string {
	return fmt.Sprintf(`{"version":1,"recording":{"displayName":"<img src=x onerror=alert(1)>","startTime":0,"endTime":1,"records":[{"type":"timeline-record-type-script","eventType":"script-evaluated","startTime":0,"endTime":%.2f}],"samples":[{"stackTraces":[{"timestamp":0.03,"stackFrames":[{"name":"renderLeaf","url":"bundle.js","line":12},{"name":"scheduleRoot","url":"bundle.js","line":2}]}],"durations":[0.01]}]}}`, end)
}

type formPart struct {
	field, filename, body string
	text                  bool
}

func post(t *testing.T, token string, parts ...formPart) *http.Request {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	if err := form.WriteField("token", token); err != nil {
		t.Fatal(err)
	}
	for _, part := range parts {
		if part.text {
			if err := form.WriteField(part.field, part.body); err != nil {
				t.Fatal(err)
			}
			continue
		}
		f, err := form.CreateFormFile(part.field, part.filename)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(f, part.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "http://"+testHost+"/", &body)
	r.Header.Set("Content-Type", form.FormDataContentType())
	r.Header.Set("Origin", "http://"+testHost)
	return r
}

func TestUploadOneOrTwoRecordings(t *testing.T) {
	h, token := openUI(t)
	base := formPart{field: "baseline", filename: "before.json", body: fixture(0.1)}
	for _, tc := range []struct {
		name     string
		other    formPart
		want     []string
		excluded string
	}{
		{name: "one", other: formPart{field: "candidate"}, want: []string{"Script total", "100 ms"}, excluded: "Baseline vs candidate"},
		{name: "two", other: formPart{field: "candidate", filename: "after.json", body: fixture(0.3)}, want: []string{"Baseline vs candidate", "after.json", "&#43;200.0%", "regressed"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, post(t, token, base, tc.other))
			body := w.Body.String()
			if w.Code != http.StatusOK {
				t.Fatalf("status %d: %.600s", w.Code, body)
			}
			for _, want := range append(tc.want, "before.json", "Hot paths and functions", "renderLeaf", "scheduleRoot", "&lt;img src=x") {
				if !strings.Contains(body, want) {
					t.Errorf("missing %q", want)
				}
			}
			for _, excluded := range []string{tc.excluded, "<img src=x", "<script"} {
				if excluded != "" && strings.Contains(body, excluded) {
					t.Errorf("unexpected %q", excluded)
				}
			}
		})
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://"+testHost+"/", nil))
	if strings.Contains(w.Body.String(), "before.json") {
		t.Fatal("subsequent GET returned a past upload")
	}
}

func TestUploadRejectsInvalidRequests(t *testing.T) {
	h, token := openUI(t)
	valid := formPart{field: "baseline", filename: "valid.json", body: fixture(0.1)}
	for _, tc := range []struct {
		name   string
		status int
		want   string
		make   func() *http.Request
	}{
		{"wrong host", 403, "loopback URL", func() *http.Request {
			r := post(t, token, valid)
			r.Host = "attacker.example"
			return r
		}},
		{"cross origin", 403, "Cross-origin", func() *http.Request {
			r := post(t, token, valid)
			r.Header.Set("Origin", "https://attacker.example")
			return r
		}},
		{"bad token", 403, "Reload", func() *http.Request { return post(t, "wrong", valid) }},
		{"path not file", 400, "not a file path", func() *http.Request {
			return post(t, token, formPart{field: "baseline", body: "/etc/passwd", text: true})
		}},
		{"missing baseline", 400, "Choose a baseline", func() *http.Request { return post(t, token) }},
		{"duplicate file", 400, "only one baseline", func() *http.Request { return post(t, token, valid, valid) }},
		{"invalid candidate", 400, "bad.json", func() *http.Request {
			return post(t, token, valid, formPart{field: "candidate", filename: "bad.json", body: `{"unknown":[]}`})
		}},
		{"invalid multipart", 400, "recording upload form", func() *http.Request {
			return httptest.NewRequest(http.MethodPost, "http://"+testHost+"/", strings.NewReader(`{"path":"/etc/passwd"}`))
		}},
		{"request too large", 413, "Upload too large", func() *http.Request {
			r := post(t, token, valid)
			r.ContentLength = 3 << 20
			return r
		}},
		{"path not served", 404, "not found", func() *http.Request {
			return httptest.NewRequest(http.MethodGet, "http://"+testHost+"/etc/passwd", nil)
		}},
		{"method not allowed", 405, "Method not allowed", func() *http.Request {
			return httptest.NewRequest(http.MethodPut, "http://"+testHost+"/", nil)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, tc.make())
			if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.want) {
				t.Errorf("status %d (want %d), missing %q: %.300s", w.Code, tc.status, tc.want, w.Body.String())
			}
		})
	}
}

func TestUploadPerFileLimitWithoutContentLength(t *testing.T) {
	h, token := openUI(t)
	var prefix, suffix bytes.Buffer
	form := multipart.NewWriter(&prefix)
	if err := form.WriteField("token", token); err != nil {
		t.Fatal(err)
	}
	if _, err := form.CreateFormFile("baseline", "big.json"); err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(&suffix, "\r\n--%s--\r\n", form.Boundary())
	body := io.MultiReader(&prefix, io.LimitReader(spaces{}, (testUploadMiB<<20)+1), &suffix)
	r := httptest.NewRequest(http.MethodPost, "http://"+testHost+"/", body)
	r.Header.Set("Content-Type", form.FormDataContentType())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d: %.300s", w.Code, w.Body.String())
	}
}

type spaces struct{}

func (spaces) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = ' '
	}
	return len(p), nil
}

func TestMissingEvidenceIsNotZero(t *testing.T) {
	h, token := openUI(t)
	base := formPart{field: "baseline", filename: "sampled.json", body: fixture(0.1)}
	cand := formPart{field: "candidate", filename: "cpu-only.json", body: `{"startTime":0,"endTime":1,"records":[{"type":"timeline-record-type-cpu","timestamp":0.5,"usage":30}]}`}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, post(t, token, base, cand))
	body := w.Body.String()
	for _, want := range []string{"<dt>Long tasks</dt><dd>Unavailable</dd>", "<dt>Long tasks</dt><dd>1</dd>", "both recordings need weighted call stacks", "renderLeaf"} {
		if !strings.Contains(body, want) {
			t.Errorf("status %d: missing %q", w.Code, want)
		}
	}
	if w.Code != http.StatusOK || strings.Contains(body, `aria-label="Sampled function changes"`) {
		t.Fatalf("status %d; function comparison should not render without both sets of samples", w.Code)
	}
}

func TestUploadRejectsExcessiveJSONStructure(t *testing.T) {
	h, token := openUI(t)
	for _, tc := range []struct {
		name, body, want string
	}{
		{"array budget", `{"records":[` + strings.Repeat("{},", maxArrayItems) + `{}]}`, "items in one JSON array"},
		{"depth budget", `{"records":[],"ignored":` + strings.Repeat("[", maxJSONDepth) + `0` + strings.Repeat("]", maxJSONDepth) + `}`, "JSON nesting limit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, post(t, token, formPart{field: "baseline", filename: "dense.json", body: tc.body}))
			if w.Code != http.StatusRequestEntityTooLarge || !strings.Contains(w.Body.String(), tc.want) {
				t.Errorf("status %d; expected %q", w.Code, tc.want)
			}
		})
	}
}
