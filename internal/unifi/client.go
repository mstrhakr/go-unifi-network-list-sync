package unifi

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
)

type apiResponse struct {
	Meta struct {
		RC  string `json:"rc"`
		Msg string `json:"msg"`
	} `json:"meta"`
	Data json.RawMessage `json:"data"`
}

// NetworkList represents a UniFi controller network list (firewall group).
type NetworkList struct {
	ID           string   `json:"_id"`
	Name         string   `json:"name"`
	GroupType    string   `json:"group_type"`
	GroupMembers []string `json:"group_members"`
	SiteID       string   `json:"site_id"`
}

// Client is an authenticated HTTP client for the UniFi controller REST API.
type Client struct {
	baseURL    string
	site       string
	httpClient *http.Client
}

// NewClient creates a client and logs into the UniFi controller.
func NewClient(baseURL, site, username, password string) (*Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("create cookie jar: %w", err)
	}

	c := &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		site:    site,
		httpClient: &http.Client{
			Jar: jar,
			Transport: &http.Transport{
				// UniFi controllers typically use self-signed certs
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		},
	}

	if err := c.login(username, password); err != nil {
		return nil, fmt.Errorf("login: %w", err)
	}
	return c, nil
}

func (c *Client) login(username, password string) error {
	payload, _ := json.Marshal(map[string]string{
		"username": username,
		"password": password,
	})
	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/api/login", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	_, err = c.doRequest(req)
	return err
}

// ListNetworkLists fetches all firewall groups (network lists) from the controller.
func (c *Client) ListNetworkLists() ([]NetworkList, error) {
	req, err := http.NewRequest(http.MethodGet, c.baseURL+"/api/s/"+c.site+"/rest/firewallgroup", nil)
	if err != nil {
		return nil, err
	}
	apiResp, err := c.doRequest(req)
	if err != nil {
		return nil, err
	}

	var groups []NetworkList
	if err := json.Unmarshal(apiResp.Data, &groups); err != nil {
		return nil, fmt.Errorf("decode network lists: %w", err)
	}
	return groups, nil
}

// GetNetworkList fetches a network list by its ID.
func (c *Client) GetNetworkList(listID string) (*NetworkList, error) {
	groups, err := c.ListNetworkLists()
	if err != nil {
		return nil, err
	}

	for i := range groups {
		if groups[i].ID == listID {
			return &groups[i], nil
		}
	}
	return nil, fmt.Errorf("network list %s not found", listID)
}

// UpdateNetworkList PUTs an updated network list back to the controller.
func (c *Client) UpdateNetworkList(nl *NetworkList) error {
	payload, err := json.Marshal(nl)
	if err != nil {
		return fmt.Errorf("encode network list: %w", err)
	}
	req, err := http.NewRequest(http.MethodPut,
		c.baseURL+"/api/s/"+c.site+"/rest/firewallgroup/"+nl.ID,
		bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	_, err = c.doRequest(req)
	return err
}

func (c *Client) doRequest(req *http.Request) (*apiResponse, error) {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var apiResp apiResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if apiResp.Meta.RC != "ok" {
		return nil, fmt.Errorf("API error: %s", apiResp.Meta.Msg)
	}

	return &apiResp, nil
}
