// Package corporateauth calls the company's credential-verification service.
package corporateauth

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Authenticator verifies credentials with the corporate identity service.
type Authenticator interface {
	Authenticate(ctx context.Context, loginID, password string) (Result, error)
}

// Result is the minimal identity returned after successful authentication.
// Adjust the JSON field names when the corporate API contract is available.
type Result struct {
	Authenticated bool   `json:"authenticated"`
	EmployeeID    string `json:"employeeId,omitempty"`
	LoginID       string `json:"loginId,omitempty"`
	FirstName     string `json:"firstName,omitempty"`
	LastName      string `json:"lastName,omitempty"`
	Email         string `json:"email,omitempty"`
	Department    string `json:"department,omitempty"`
}

type authenticationResponse struct {
	Data struct {
		Status   string `json:"status"`
		UserInfo struct {
			FirstName  string `json:"firstName"`
			LastName   string `json:"lastName"`
			Email      string `json:"email"`
			Department string `json:"department"`
			ID         string `json:"id"`
		} `json:"userInfo"`
	} `json:"data"`
}

// Client is an HTTP implementation of Authenticator.
type Client struct {
	endpoint   string
	httpClient *http.Client
	apiKey     string
}

// NewClient creates a corporate authentication client.
func NewClient(endpoint, apiKey string, httpClient *http.Client) (*Client, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return nil, errors.New("corporate authentication endpoint is required")
	}
	if httpClient == nil {
		return nil, errors.New("HTTP client is required")
	}
	return &Client{
		endpoint:   endpoint,
		apiKey:     strings.TrimSpace(apiKey),
		httpClient: httpClient,
	}, nil
}

// Authenticate posts credentials to the corporate REST API. Passwords are
// sent only in the request body and must never be written to application logs.
func (c *Client) Authenticate(ctx context.Context, loginID, password string) (Result, error) {
	encodedPassword := base64.StdEncoding.EncodeToString([]byte(password))
	payload, err := json.Marshal(struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}{Username: strings.TrimSpace(loginID), Password: encodedPassword})
	if err != nil {
		return Result{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(payload))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	response, err := c.httpClient.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("call corporate authentication API: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return Result{Authenticated: false}, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return Result{}, fmt.Errorf("corporate authentication API returned HTTP %d", response.StatusCode)
	}

	var apiResponse authenticationResponse
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	if err := decoder.Decode(&apiResponse); err != nil {
		return Result{}, fmt.Errorf("decode corporate authentication response: %w", err)
	}
	return Result{
		Authenticated: strings.EqualFold(strings.TrimSpace(apiResponse.Data.Status), "success"),
		EmployeeID:    strings.TrimSpace(apiResponse.Data.UserInfo.ID),
		LoginID:       strings.TrimSpace(loginID),
		FirstName:     strings.TrimSpace(apiResponse.Data.UserInfo.FirstName),
		LastName:      strings.TrimSpace(apiResponse.Data.UserInfo.LastName),
		Email:         strings.TrimSpace(apiResponse.Data.UserInfo.Email),
		Department:    strings.TrimSpace(apiResponse.Data.UserInfo.Department),
	}, nil
}
