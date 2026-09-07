package scan

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/blackbirdworks/gopowerwall/models"
	"github.com/blackbirdworks/gopowerwall/pkgs/version"
)

var errCastUDP = errors.New("failed to cast udp address")

const (
	// Unknown indicates an unidentified property.
	Unknown = "Unknown"
	// Powerwall represents the Powerwall device model.
	Powerwall = "Powerwall"

	defaultHTTPSPort   = 443
	defaultMaxThreads  = 30
	maxAllowedThreads  = 256
	defaultTimeout     = 1 * time.Second
	clientTimeout      = 5 * time.Second
	warningTimeout     = 200 * time.Millisecond
	defaultFallbackNet = "192.168.1.0/24"
	//nolint:gosec // Not a credential, public info link
	pw3SupportURL    = "https://tinyurl.com/pw3support"
	statusURLPattern = "https://%s/api/status"
	tedapiDINPattern = "https://%s/tedapi/din"
)

// Context handles terminal formatting, color and interactive output.
type Context struct {
	Output      io.Writer
	Timeout     time.Duration
	Color       bool
	Interactive bool
}

// NewContext creates a new scan formatting context.
func NewContext(timeout time.Duration, color, interactive bool, output io.Writer) *Context {
	if !interactive {
		color = false
	}
	if output == nil {
		output = os.Stdout
	}

	return &Context{
		Timeout:     timeout,
		Color:       color,
		Interactive: interactive,
		Output:      output,
	}
}

func (c *Context) Bold() string {
	if c.Color {
		return "\033[0m\033[97m\033[1m"
	}

	return ""
}

func (c *Context) SubBold() string {
	if c.Color {
		return "\033[0m\033[32m"
	}

	return ""
}

func (c *Context) Normal() string {
	if c.Color {
		return "\033[97m\033[0m"
	}

	return ""
}

func (c *Context) Dim() string {
	if c.Color {
		return "\033[0m\033[97m\033[2m"
	}

	return ""
}

func (c *Context) Alert() string {
	if c.Color {
		return "\033[0m\033[91m\033[1m"
	}

	return ""
}

// GetMyIP determines local outbound IPv4 address.
func GetMyIP(ctx context.Context) (string, error) {
	dialer := &net.Dialer{}
	conn, err := dialer.DialContext(ctx, "udp", "8.8.8.8:80")
	if err != nil {
		return "", fmt.Errorf("dial outbound: %w", err)
	}
	defer func() {
		_ = conn.Close()
	}()

	udpAddr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		return "", errCastUDP
	}

	return udpAddr.IP.String(), nil
}

// CheckConnection probes TCP connection on specified port or 443.
func CheckConnection(ctx context.Context, addr string, timeout time.Duration, port int) bool {
	host := addr
	targetPort := port
	if h, pStr, err := net.SplitHostPort(addr); err == nil {
		host = h
		if p, convErr := strconv.Atoi(pStr); convErr == nil {
			targetPort = p
		}
	}
	if targetPort <= 0 {
		targetPort = defaultHTTPSPort
	}
	target := net.JoinHostPort(host, strconv.Itoa(targetPort))

	dialer := &net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "tcp", target)
	if err != nil {
		return false
	}
	_ = conn.Close()

	return true
}

func incIP(ip net.IP) {
	for j := len(ip) - 1; j >= 0; j-- {
		ip[j]++
		if ip[j] > 0 {
			return
		}
	}
}

// Hosts returns all host IPs in the given CIDR network.
func Hosts(cidr string) ([]string, error) {
	ip, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, fmt.Errorf("parse cidr: %w", err)
	}
	var ips []string
	curr := make(net.IP, len(ip.To4()))
	copy(curr, ip.To4())

	for hostIP := curr.Mask(ipnet.Mask); ipnet.Contains(hostIP); incIP(hostIP) {
		ips = append(ips, hostIP.String())
	}
	ones, bits := ipnet.Mask.Size()
	if bits == 32 && ones < 31 && len(ips) > 2 {
		return ips[1 : len(ips)-1], nil
	}

	return ips, nil
}

type statusPayload struct {
	DIN           string `json:"din"`
	Version       string `json:"version"`
	UpTimeSeconds string `json:"up_time_seconds"`
	TEGType       string `json:"teg_type"`
}

func checkStatus(
	ctx context.Context,
	addr string,
	sCtx *Context,
	client *http.Client,
) (*models.DiscoveredDevice, string) {
	url := fmt.Sprintf(statusURLPattern, addr)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, ""
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, ""
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode == http.StatusNotFound {
		return nil, ""
	}

	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, ""
	}

	var data statusPayload
	if unmarshalErr := json.Unmarshal(body, &data); unmarshalErr != nil {
		return nil, ""
	}

	din := data.DIN
	if din == "" {
		din = Unknown
	}
	fw := data.Version
	if fw == "" {
		fw = Unknown
	}

	if din == Unknown && fw == Unknown {
		return nil, ""
	}

	tegType := data.TEGType
	if strings.EqualFold(tegType, Unknown) || tegType == "" {
		tegType = Powerwall
	}

	msg := fmt.Sprintf("OPEN%s - %sFound %s %s%s\n\t\t\t\t\t [Firmware %s]%s",
		sCtx.Dim(), sCtx.SubBold(), tegType, din, sCtx.SubBold(), fw, sCtx.Normal())

	return &models.DiscoveredDevice{
		IP:          addr,
		DIN:         din,
		Version:     fw,
		DeviceType:  tegType,
		IsPowerwall: true,
	}, msg
}

func checkPW3(ctx context.Context, addr string, sCtx *Context, client *http.Client) (*models.DiscoveredDevice, string) {
	url := fmt.Sprintf(tedapiDINPattern, addr)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, ""
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, ""
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, ""
	}

	if strings.Contains(string(body), "User does not have adequate access rights") {
		msg := fmt.Sprintf("OPEN%s - %sFound Powerwall 3 [Cloud and TEDAPI Mode only]%s",
			sCtx.Dim(), sCtx.SubBold(), sCtx.Normal())

		return &models.DiscoveredDevice{
			IP:          addr,
			DIN:         "Powerwall-3",
			Version:     "Cloud and TEDAPI Mode support only - See " + pw3SupportURL,
			DeviceType:  Powerwall,
			IsPowerwall: true,
		}, msg
	}

	return nil, ""
}

// IP probes a single IP for gateway signatures.
func IP(
	ctx context.Context,
	addr string,
	sCtx *Context,
	httpClient *http.Client,
) (*models.DiscoveredDevice, string) {
	if !CheckConnection(ctx, addr, sCtx.Timeout, defaultHTTPSPort) {
		return nil, ""
	}

	if dev, msg := checkStatus(ctx, addr, sCtx, httpClient); dev != nil {
		return dev, msg
	}

	return checkPW3(ctx, addr, sCtx, httpClient)
}

func resolveCIDR(ctx context.Context, sCtx *Context, opts *models.ScanOptions) string {
	cidr := opts.CIDR
	if cidr == "" && opts.IP != "" {
		cidr = opts.IP
	}
	if cidr == "" {
		myIP, err := GetMyIP(ctx)
		if err != nil {
			if sCtx.Interactive {
				fmt.Fprintf(sCtx.Output, "%sERROR: Unable to determine your IP address and network automatically.%s\n",
					sCtx.Alert(), sCtx.Normal())
			}
			cidr = defaultFallbackNet
		} else {
			cidr = myIP
		}
	}
	if !strings.Contains(cidr, "/") {
		cidr += "/24"
	}

	return cidr
}

func printScanHeader(sCtx *Context, netStr string) {
	if !sCtx.Interactive {
		return
	}
	fmt.Fprintf(
		sCtx.Output,
		"%s\ngopowerwall Network Scanner%s [%s]%s\n",
		sCtx.Bold(),
		sCtx.Dim(),
		version.Version,
		sCtx.Normal(),
	)
	fmt.Fprintf(sCtx.Output, "%sScan local network for Tesla Powerwall Gateways\n\n", sCtx.Dim())
	if sCtx.Timeout < warningTimeout {
		fmt.Fprintf(
			sCtx.Output,
			"%s\tWARNING: Setting a low timeout (%v) may cause misses.\n\n",
			sCtx.Alert(),
			sCtx.Timeout,
		)
	}
	fmt.Fprintf(
		sCtx.Output,
		"%s\tRunning Scan on %s%s%s...%s\n",
		sCtx.Bold(),
		sCtx.SubBold(),
		netStr,
		sCtx.Bold(),
		sCtx.Dim(),
	)
}

func printScanSummary(sCtx *Context, results []models.DiscoveredDevice) {
	if !sCtx.Interactive {
		return
	}
	fmt.Fprintf(sCtx.Output, "%s\r\t  Done\t\t\t\t\t\t   \n%s", sCtx.Dim(), sCtx.Normal())
	fmt.Fprintf(sCtx.Output, "%sDiscovered %d Powerwall Gateway(s)\n", sCtx.Normal(), len(results))
	for _, dev := range results {
		fmt.Fprintf(sCtx.Output, "%s\t %s [%s] Firmware %s\n", sCtx.Dim(), dev.IP, dev.DIN, dev.Version)
	}
	fmt.Fprintln(sCtx.Output, sCtx.Normal())
}

// Scan searches the local subnet or specified CIDR for Tesla Powerwall Gateways.
func Scan(ctx context.Context, opts models.ScanOptions, out io.Writer) ([]models.DiscoveredDevice, error) {
	timeout := time.Duration(opts.TimeoutSec * float64(time.Second))
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	sCtx := NewContext(timeout, opts.Color, opts.Interactive, out)

	cidr := resolveCIDR(ctx, sCtx, &opts)

	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, fmt.Errorf("invalid CIDR: %w", err)
	}

	printScanHeader(sCtx, ipnet.String())

	hosts, err := Hosts(cidr)
	if err != nil {
		return nil, err
	}

	//nolint:gosec // Powerwall uses self-signed certs
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		DialContext:     (&net.Dialer{Timeout: clientTimeout}).DialContext,
	}
	httpClient := &http.Client{Transport: tr, Timeout: clientTimeout}

	threads := opts.MaxHosts
	if threads <= 0 {
		threads = defaultMaxThreads
	}
	if threads > maxAllowedThreads {
		threads = maxAllowedThreads
	}

	var (
		mu      sync.Mutex
		outMu   sync.Mutex
		wg      sync.WaitGroup
		results []models.DiscoveredDevice
	)
	sem := make(chan struct{}, threads)

	for _, host := range hosts {
		wg.Add(1)
		sem <- struct{}{}
		go func(addr string) {
			defer func() {
				<-sem
				wg.Done()
			}()

			if sCtx.Interactive {
				outMu.Lock()
				fmt.Fprintf(sCtx.Output, "%s\r\t  Host: %s%s ...%s", sCtx.Dim(), sCtx.SubBold(), addr, sCtx.Normal())
				outMu.Unlock()
			}

			dev, msg := IP(ctx, addr, sCtx, httpClient)
			if dev != nil {
				mu.Lock()
				results = append(results, *dev)
				mu.Unlock()
				if sCtx.Interactive {
					hostLine := fmt.Sprintf("%s\r\t  Host: %s%s ...%s", sCtx.Dim(), sCtx.SubBold(), addr, sCtx.Normal())
					outMu.Lock()
					fmt.Fprintf(sCtx.Output, "%s %s\n", hostLine, msg)
					outMu.Unlock()
				}
			}
		}(host)
	}

	wg.Wait()
	printScanSummary(sCtx, results)

	return results, nil
}
