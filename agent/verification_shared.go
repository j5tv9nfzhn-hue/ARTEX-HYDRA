package agent

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// verificationFlowCapture 是验证链路单次响应允许读取的最大字节数。
const verificationFlowCapture = 32768

// verificationOrigin 归一化并校验验证流程的目标源（scheme + host[:port]）。
func verificationOrigin(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") || len(raw) > 2048 {
		return "", fmt.Errorf("invalid HTTP origin")
	}
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if (u.Scheme == "https" && port == "443") || (u.Scheme == "http" && port == "80") {
		port = ""
	}
	if port != "" {
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return u.Scheme + "://" + host, nil
}

// verificationBoundedTransport 克隆一份只属于当前验证链路的 transport。
func verificationBoundedTransport(base *http.Transport) *http.Transport {
	tr := base.Clone()
	tr.MaxResponseHeaderBytes = verificationFlowCapture
	return tr
}

// verificationFlowResult 是验证求解状态缓存的共享结果类型。
type verificationFlowResult struct {
	FailureCode                 string   `json:"failure_code,omitempty"`
	Protocol                    string   `json:"protocol,omitempty"`
	RequestedFingerprintProfile string   `json:"requested_fingerprint_profile,omitempty"`
	TransportProvider           string   `json:"transport_provider,omitempty"`
	State                       string   `json:"state"`
	Stage                       string   `json:"stage"`
	Requests                    int      `json:"requests"`
	ExchangeIDs                 []string `json:"exchange_ids"`
	Category                    string   `json:"category,omitempty"`
	SubmissionAttempted         bool     `json:"submission_attempted"`
	BusinessConfirmed           bool     `json:"business_confirmed"`
	Cached                      bool     `json:"cached"`
	Reason                      string   `json:"reason"`
}
