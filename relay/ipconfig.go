package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

const requiredIPServiceMatches = 2

type ipService struct {
	name      string
	url       string
	normalize func([]byte) (net.IP, error)
}

var publicIPServices = []ipService{
	{name: "ipify", url: "https://api.ipify.org?format=json", normalize: normalizeJSONIP},
	{name: "Amazon", url: "https://checkip.amazonaws.com", normalize: normalizePlainIP},
	{name: "icanhazip", url: "https://ipv4.icanhazip.com", normalize: normalizePlainIP},
	{name: "ifconfig.me", url: "https://ifconfig.me/ip", normalize: normalizePlainIP},
}

func (s ipService) discover(ctx context.Context, client *http.Client) (net.IP, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json, text/plain")
	request.Header.Set("User-Agent", "kwebbel-relay/1")

	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s returned HTTP %d", s.name, response.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, 1024))
	if err != nil {
		return nil, err
	}
	return s.normalize(body)
}

func autodiscoverIP() (net.IP, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	client := &http.Client{Timeout: 5 * time.Second}
	return discoverPublicIP(ctx, client, publicIPServices)
}

func discoverPublicIP(ctx context.Context, client *http.Client, services []ipService) (net.IP, error) {
	results := make(chan net.IP, len(services))
	for _, service := range services {
		go func() {
			ip, err := service.discover(ctx, client)
			if err == nil {
				results <- ip
				return
			}
			results <- nil
		}()
	}

	reports := make(map[string]int)
	for range services {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("public IP discovery timed out: %w", ctx.Err())
		case ip := <-results:
			if ip == nil {
				continue
			}
			ipString := ip.String()
			reports[ipString]++
			if reports[ipString] >= requiredIPServiceMatches {
				return ip, nil
			}
		}
	}
	return nil, errors.New("public IP services did not produce two matching results")
}

func normalizePlainIP(body []byte) (net.IP, error) {
	return parsePublicIPv4(strings.TrimSpace(string(body)))
}

func normalizeJSONIP(body []byte) (net.IP, error) {
	var response struct {
		IP string `json:"ip"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, err
	}
	return parsePublicIPv4(response.IP)
}

func parsePublicIPv4(value string) (net.IP, error) {
	ip := net.ParseIP(strings.TrimSpace(value))
	if ip == nil || ip.To4() == nil {
		return nil, fmt.Errorf("%q is not an IPv4 address", value)
	}
	ip = ip.To4()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() {
		return nil, fmt.Errorf("%s is not a public IPv4 address", ip)
	}
	return ip, nil
}
