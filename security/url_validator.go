// Package security provides security utilities for the application
package security

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Private/Reserved IP ranges that should be blocked to prevent SSRF
var privateIPBlocks []*net.IPNet

func init() {
	// Initialize private IP blocks
	// These ranges should not be accessible via user-controlled URLs
	privateRanges := []string{
		"127.0.0.0/8",    // IPv4 loopback
		"10.0.0.0/8",     // RFC1918 private
		"172.16.0.0/12",  // RFC1918 private
		"192.168.0.0/16", // RFC1918 private
		"169.254.0.0/16", // Link-local / Cloud metadata
		"0.0.0.0/8",      // Current network
		"224.0.0.0/4",    // Multicast
		"240.0.0.0/4",    // Reserved
		"::1/128",        // IPv6 loopback
		"fe80::/10",      // IPv6 link-local
		"fc00::/7",       // IPv6 unique local
	}

	for _, cidr := range privateRanges {
		_, block, err := net.ParseCIDR(cidr)
		if err == nil {
			privateIPBlocks = append(privateIPBlocks, block)
		}
	}
}

// SSRFError represents a Server-Side Request Forgery attempt
type SSRFError struct {
	URL    string
	Reason string
}

func (e *SSRFError) Error() string {
	return fmt.Sprintf("SSRF blocked: %s - %s", e.URL, e.Reason)
}

// isPrivateIP checks if an IP address is in a private/reserved range
func isPrivateIP(ip net.IP) bool {
	if ip == nil {
		return true // Invalid IP, treat as private
	}

	// Check if it's a loopback address
	if ip.IsLoopback() {
		return true
	}

	// Check if it's a link-local address
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}

	// Check if it's a private address
	if ip.IsPrivate() {
		return true
	}

	// Check against our explicit private ranges
	for _, block := range privateIPBlocks {
		if block.Contains(ip) {
			return true
		}
	}

	return false
}

// ValidateURL checks if a URL is safe to request (not pointing to internal networks)
// Returns an error if the URL is potentially dangerous
func ValidateURL(rawURL string) error {
	if rawURL == "" {
		return &SSRFError{URL: rawURL, Reason: "empty URL"}
	}

	// Parse the URL
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return &SSRFError{URL: rawURL, Reason: "invalid URL format"}
	}

	// Only allow http and https schemes
	scheme := strings.ToLower(parsedURL.Scheme)
	if scheme != "http" && scheme != "https" {
		return &SSRFError{URL: rawURL, Reason: fmt.Sprintf("unsupported scheme: %s", scheme)}
	}

	// Extract hostname (without port)
	host := parsedURL.Hostname()
	if host == "" {
		return &SSRFError{URL: rawURL, Reason: "empty hostname"}
	}

	// Block localhost and common internal hostnames
	lowerHost := strings.ToLower(host)
	blockedHosts := []string{
		"localhost",
		"127.0.0.1",
		"::1",
		"0.0.0.0",
		"metadata.google.internal",
		"metadata.google",
		"instance-data",
	}
	for _, blocked := range blockedHosts {
		if lowerHost == blocked {
			return &SSRFError{URL: rawURL, Reason: fmt.Sprintf("blocked hostname: %s", host)}
		}
	}

	// Resolve the hostname to IP addresses
	// This catches DNS rebinding and ensures we check the actual destination
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resolver := net.Resolver{}
	ips, err := resolver.LookupIPAddr(ctx, host)
	if err != nil {
		// If DNS resolution fails, we still need to check if it's an IP address directly
		ip := net.ParseIP(host)
		if ip != nil {
			if isPrivateIP(ip) {
				return &SSRFError{URL: rawURL, Reason: "resolves to private IP address"}
			}
			return nil // It's a valid public IP
		}
		// DNS resolution failed, but it's not an IP - could be a typo or non-existent domain
		// Allow it and let the HTTP client handle the error
		return nil
	}

	// Check all resolved IPs
	for _, ipAddr := range ips {
		if isPrivateIP(ipAddr.IP) {
			return &SSRFError{URL: rawURL, Reason: fmt.Sprintf("resolves to private IP: %s", ipAddr.IP)}
		}
	}

	return nil
}

type modelOrigin struct {
	scheme string
	host   string
	port   string
}

func modelURLOrigin(u *url.URL) (modelOrigin, bool) {
	scheme := strings.ToLower(u.Scheme)
	if u.User != nil || u.Hostname() == "" || (scheme != "http" && scheme != "https") {
		return modelOrigin{}, false
	}
	port := u.Port()
	if port == "" {
		port = "80"
		if scheme == "https" {
			port = "443"
		}
	}
	return modelOrigin{scheme: scheme, host: strings.ToLower(u.Hostname()), port: port}, true
}

// trustedModelOrigin only trusts origins explicitly configured by the operator.
// The default is empty; model configuration supplied by a user cannot grant trust.
func trustedModelOrigin(target *url.URL) *modelOrigin {
	wanted, ok := modelURLOrigin(target)
	if !ok {
		return nil
	}
	for _, entry := range strings.Split(os.Getenv("NOFX_TRUSTED_MODEL_ORIGINS"), ",") {
		configured, err := url.Parse(strings.TrimSpace(entry))
		if err != nil || (configured.Path != "" && configured.Path != "/") || configured.RawQuery != "" || configured.ForceQuery || configured.Fragment != "" {
			continue
		}
		origin, valid := modelURLOrigin(configured)
		if valid && origin == wanted {
			return &origin
		}
	}
	return nil
}

// ValidateModelURL validates model endpoints. Private origins require an explicit
// NOFX_TRUSTED_MODEL_ORIGINS entry; all other destinations use normal SSRF rules.
func ValidateModelURL(rawURL string) error {
	cleanURL := strings.TrimSuffix(strings.TrimSpace(rawURL), "#")
	parsedURL, err := url.Parse(cleanURL)
	if err != nil {
		return &SSRFError{URL: rawURL, Reason: "invalid URL format"}
	}

	if trustedModelOrigin(parsedURL) != nil {
		return nil
	}

	return ValidateURL(cleanURL)
}

// SafeHTTPClient returns an HTTP client with SSRF protection
// It validates URLs and blocks requests to private networks
func SafeHTTPClient(timeout time.Duration) *http.Client {
	return safeHTTPClient(timeout, nil)
}

// SafeHTTPClientForModelURL returns an SSRF-protected client for a validated
// model endpoint. Any operator-configured exception is limited to this origin.
func SafeHTTPClientForModelURL(rawURL string, timeout time.Duration) (*http.Client, error) {
	if err := ValidateModelURL(rawURL); err != nil {
		return nil, err
	}
	parsedURL, _ := url.Parse(strings.TrimSuffix(strings.TrimSpace(rawURL), "#"))
	return safeHTTPClient(timeout, trustedModelOrigin(parsedURL)), nil
}

func safeHTTPClient(timeout time.Duration, allowedOrigin *modelOrigin) *http.Client {
	dialer := &net.Dialer{
		Timeout:   timeout,
		KeepAlive: 30 * time.Second,
	}

	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			// Extract host from address
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				host = addr
			}

			port := ""
			if _, parsedPort, splitErr := net.SplitHostPort(addr); splitErr == nil {
				port = parsedPort
			}

			// Resolve and check the IP. Only the configured model origin may
			// resolve to a private address.
			ips, err := net.LookupIP(host)
			if err != nil {
				return nil, fmt.Errorf("SSRF protection: failed to resolve host %s: %w", host, err)
			}

			for _, ip := range ips {
				if isPrivateIP(ip) && !(allowedOrigin != nil && strings.EqualFold(host, allowedOrigin.host) && port == allowedOrigin.port) {
					return nil, fmt.Errorf("SSRF protection: blocked connection to private IP %s", ip)
				}
			}

			return dialer.DialContext(ctx, network, addr)
		},
	}

	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("too many redirects")
			}

			// Validate the redirect URL
			if origin, ok := modelURLOrigin(req.URL); ok && allowedOrigin != nil && origin == *allowedOrigin {
				return nil
			}
			if err := ValidateURL(req.URL.String()); err != nil {
				return fmt.Errorf("SSRF protection: redirect blocked - %w", err)
			}

			return nil
		},
	}
}

// SafeGet performs a GET request with SSRF protection
// It validates the URL before making the request and uses a safe HTTP client
func SafeGet(rawURL string, timeout time.Duration) (*http.Response, error) {
	// First validate the URL
	if err := ValidateURL(rawURL); err != nil {
		return nil, err
	}

	// Use the safe HTTP client
	client := SafeHTTPClient(timeout)
	return client.Get(rawURL)
}
