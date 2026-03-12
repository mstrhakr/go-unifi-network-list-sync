package unifi

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// paginatedResponse is the wrapper for list endpoints in the integration API.
type paginatedResponse struct {
	Offset     int             `json:"offset"`
	Limit      int             `json:"limit"`
	Count      int             `json:"count"`
	TotalCount int             `json:"totalCount"`
	Data       json.RawMessage `json:"data"`
}

// Site represents a UniFi site.
type Site struct {
	ID                string `json:"id"`
	InternalReference string `json:"internalReference"`
	Name              string `json:"name"`
}

// NetworkList represents a traffic matching list (the UniFi integration API
// calls these "traffic matching lists"; the UI calls them "network lists").
type NetworkList struct {
	Type  string             `json:"type"` // PORTS, IPV4_ADDRESSES, IPV6_ADDRESSES
	ID    string             `json:"id"`
	Name  string             `json:"name"`
	Items []TrafficMatchItem `json:"items,omitempty"`
}

// TrafficMatchItem represents an item in a traffic matching list.
// For IPV4_ADDRESSES: type is IP_ADDRESS (value), SUBNET (value), or IP_ADDRESS_RANGE (start/stop).
type TrafficMatchItem struct {
	Type  string `json:"type"`
	Value string `json:"value,omitempty"`
	Start string `json:"start,omitempty"`
	Stop  string `json:"stop,omitempty"`
}

// networkListUpdate is the request body for creating/updating a traffic matching list.
type networkListUpdate struct {
	Type  string             `json:"type"`
	Name  string             `json:"name"`
	Items []TrafficMatchItem `json:"items"`
}

// Client is an authenticated HTTP client for the UniFi integration API.
type Client struct {
	baseURL    string
	site       string
	siteID     string // resolved UUID
	apiKey     string
	httpClient *http.Client
}

// NewClient creates a client that authenticates via API key.
func NewClient(baseURL, site, apiKey string) (*Client, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("API key is required")
	}

	c := &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		site:    site,
		apiKey:  apiKey,
		httpClient: &http.Client{
			Transport: &http.Transport{
				// UniFi controllers typically use self-signed certs
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		},
	}
	return c, nil
}

// resolveSiteID resolves the configured site identifier to a UUID.
func (c *Client) resolveSiteID() (string, error) {
	if c.siteID != "" {
		return c.siteID, nil
	}

	// If the site value already looks like a UUID, use it directly.
	if len(c.site) == 36 && strings.Count(c.site, "-") == 4 {
		c.siteID = c.site
		return c.siteID, nil
	}

	body, err := c.doRequest(http.MethodGet, "/integration/v1/sites?limit=200", nil)
	if err != nil {
		return "", fmt.Errorf("list sites: %w", err)
	}

	var page paginatedResponse
	if err := json.Unmarshal(body, &page); err != nil {
		return "", fmt.Errorf("decode sites response: %w", err)
	}

	var sites []Site
	if err := json.Unmarshal(page.Data, &sites); err != nil {
		return "", fmt.Errorf("decode sites: %w", err)
	}

	for _, s := range sites {
		if s.InternalReference == c.site || s.Name == c.site || s.ID == c.site {
			c.siteID = s.ID
			return c.siteID, nil
		}
	}

	return "", fmt.Errorf("site %q not found (available: %d sites)", c.site, len(sites))
}

// ListNetworkLists fetches all traffic matching lists from the controller.
func (c *Client) ListNetworkLists() ([]NetworkList, error) {
	siteID, err := c.resolveSiteID()
	if err != nil {
		return nil, err
	}

	body, err := c.doRequest(http.MethodGet,
		"/integration/v1/sites/"+siteID+"/traffic-matching-lists?limit=200", nil)
	if err != nil {
		return nil, err
	}

	var page paginatedResponse
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, fmt.Errorf("decode traffic matching lists response: %w", err)
	}

	var lists []NetworkList
	if err := json.Unmarshal(page.Data, &lists); err != nil {
		return nil, fmt.Errorf("decode traffic matching lists: %w", err)
	}
	return lists, nil
}

// GetNetworkList fetches a traffic matching list by its ID.
func (c *Client) GetNetworkList(listID string) (*NetworkList, error) {
	siteID, err := c.resolveSiteID()
	if err != nil {
		return nil, err
	}

	body, err := c.doRequest(http.MethodGet,
		"/integration/v1/sites/"+siteID+"/traffic-matching-lists/"+listID, nil)
	if err != nil {
		return nil, err
	}

	var nl NetworkList
	if err := json.Unmarshal(body, &nl); err != nil {
		return nil, fmt.Errorf("decode traffic matching list: %w", err)
	}
	return &nl, nil
}

// UpdateNetworkList PUTs an updated traffic matching list back to the controller.
func (c *Client) UpdateNetworkList(nl *NetworkList) error {
	siteID, err := c.resolveSiteID()
	if err != nil {
		return err
	}

	update := networkListUpdate{
		Type:  nl.Type,
		Name:  nl.Name,
		Items: nl.Items,
	}
	payload, err := json.Marshal(update)
	if err != nil {
		return fmt.Errorf("encode traffic matching list: %w", err)
	}

	_, err = c.doRequest(http.MethodPut,
		"/integration/v1/sites/"+siteID+"/traffic-matching-lists/"+nl.ID, payload)
	return err
}

func (c *Client) doRequest(method, path string, body []byte) ([]byte, error) {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}

	req, err := http.NewRequest(method, c.baseURL+path, bodyReader)
	if err != nil {
		return nil, err
	}

	req.Header.Set("X-API-Key", c.apiKey)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet := string(respBody)
		if len(snippet) > 200 {
			snippet = snippet[:200] + "..."
		}
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, snippet)
	}

	// If the response is HTML (e.g. a login redirect), the API key is wrong
	// or the URL is pointing at the wrong endpoint.
	ct := resp.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "text/html") || (len(respBody) > 0 && respBody[0] == '<') {
		snippet := string(respBody)
		if len(snippet) > 200 {
			snippet = snippet[:200] + "..."
		}
		return nil, fmt.Errorf("controller returned HTML instead of JSON (check URL and API key): %s", snippet)
	}

	return respBody, nil
}
