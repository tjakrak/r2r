package corporateauth

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestClientAuthenticate(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("unexpected request method or authorization header")
		}
		var request struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Username != "ryan.t" || request.Password != "c2VjcmV0" {
			t.Fatalf("unexpected authentication payload: %+v", request)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"data":{"status":"success","userInfo":{"firstName":"Ryan","lastName":"Tjakrakartadinata","email":"ryan@example.test","department":"AAA","id":"E300"}}}`)),
			Header:     make(http.Header),
		}, nil
	})}

	client, err := NewClient("https://authentication.example.test", "test-key", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Authenticate(context.Background(), "ryan.t", "secret")
	if err != nil || !result.Authenticated || result.EmployeeID != "E300" || result.FirstName != "Ryan" || result.Department != "AAA" {
		t.Fatalf("unexpected result: %+v, %v", result, err)
	}
}

func TestClientAuthenticateRejectsInvalidCredentials(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusUnauthorized,
			Body:       io.NopCloser(strings.NewReader("")),
			Header:     make(http.Header),
		}, nil
	})}
	client, err := NewClient("https://authentication.example.test", "", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Authenticate(context.Background(), "ryan.t", "wrong")
	if err != nil || result.Authenticated {
		t.Fatalf("expected rejected credentials, got %+v, %v", result, err)
	}
}

func TestClientAuthenticateEncodesUTF8PasswordLocally(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost {
			t.Fatalf("request method = %s, want POST", r.Method)
		}
		var request struct {
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Password != "cMOkc3Mv5a+G56CB" {
			t.Fatalf("validation password = %q, want UTF-8 Base64 value", request.Password)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"data":{"status":"success","userInfo":{"id":""}}}`)),
			Header:     make(http.Header),
		}, nil
	})}
	client, err := NewClient(
		"http://hwtb1web01.headway.com/HWT.API/validateuser",
		"",
		httpClient,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Authenticate(context.Background(), "ryan.t", "päss/密码"); err != nil {
		t.Fatal(err)
	}
}
