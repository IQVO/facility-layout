package http

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestContainsNUL(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{"plain", `{"a":"b"}`, false},
		{"raw NUL byte", "{\"a\":\"b\x00\"}", true},
		{"escape", `{"a":"b\u0000"}`, true},
		{"escape first", `{"\u0000":"b"}`, true},
		{"escaped backslash then text is not a NUL", `{"a":"b\\u0000"}`, false},
		{"three backslashes is an escaped backslash then an escape", `{"a":"b\\\u0000"}`, true},
		{"later real escape after a false one", `{"a":"\\u0000","b":"\u0000"}`, true},
		{"other control escapes are fine", `{"a":"\u0001\u001f"}`, false},
		{"empty", ``, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := containsNUL([]byte(tt.body)); got != tt.want {
				t.Errorf("containsNUL(%q) = %v, want %v", tt.body, got, tt.want)
			}
		})
	}
}

func TestRejectNUL(t *testing.T) {
	var gotBody string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusNoContent)
	})
	h := rejectNUL(next)

	tests := []struct {
		name       string
		method     string
		target     string
		body       string
		wantStatus int
		wantNext   bool
	}{
		{"clean POST reaches the handler with its body intact", http.MethodPost, "/zones/Z1/aisles", `{"aisleCode":"A1"}`, http.StatusNoContent, true},
		{"clean GET", http.MethodGet, "/sites/WH1", "", http.StatusNoContent, true},
		{"NUL escape in a JSON body is a 400", http.MethodPost, "/placement-rules", `{"locationType":"a\u0000b"}`, http.StatusBadRequest, false},
		{"raw NUL in a JSON body is a 400", http.MethodPut, "/locations/X/geometry", "{\"a\":\"\x00\"}", http.StatusBadRequest, false},
		{"NUL in the path is a 404", http.MethodGet, "/sites/a%00b", "", http.StatusNotFound, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotBody = ""
			var body io.Reader = http.NoBody
			if tt.body != "" {
				body = strings.NewReader(tt.body)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(tt.method, tt.target, body))
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if tt.wantNext {
				if gotBody != tt.body {
					t.Errorf("handler saw body %q, want %q", gotBody, tt.body)
				}
				return
			}
			if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
				t.Errorf("Content-Type = %q, want application/problem+json", ct)
			}
			if gotBody != "" {
				t.Errorf("handler was reached with body %q", gotBody)
			}
		})
	}
}
