package download

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/pkg/sftp"
	"github.com/sonic-net/sonic-gnmi/pkg/hostfs"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

const (
	defaultRemoteTimeout = 15 * time.Minute
	defaultSSHPort       = "22"
	maxSCPControlLine    = 4096
)

var defaultKnownHostsFiles = []string{
	"/etc/ssh/ssh_known_hosts",
	"/root/.ssh/known_hosts",
}

// ErrInvalidRequest identifies a remote download request that cannot be used.
var ErrInvalidRequest = errors.New("invalid remote download request")

// Protocol identifies a supported remote download protocol.
type Protocol string

const (
	ProtocolSFTP  Protocol = "SFTP"
	ProtocolHTTP  Protocol = "HTTP"
	ProtocolHTTPS Protocol = "HTTPS"
	ProtocolSCP   Protocol = "SCP"
)

// Request describes a remote file transfer.
type Request struct {
	Protocol Protocol
	Path     string
	Username string
	Password string
	MaxSize  int64
}

type remoteClient struct {
	httpTransport   http.RoundTripper
	knownHostsFiles []string
	dialContext     func(context.Context, string, string) (net.Conn, error)
	timeout         time.Duration
}

type remoteTarget struct {
	httpURL    *url.URL
	sshAddress string
	remotePath string
}

func newRemoteClient() *remoteClient {
	return newRemoteClientWithHostPath(hostfs.Translate)
}

func newRemoteClientWithHostPath(translateHostPath func(string) string) *remoteClient {
	dialer := &net.Dialer{}
	knownHostsFiles := make([]string, 0, len(defaultKnownHostsFiles))
	for _, knownHostsFile := range defaultKnownHostsFiles {
		knownHostsFiles = append(knownHostsFiles, translateHostPath(knownHostsFile))
	}
	return &remoteClient{
		httpTransport:   newHTTPTransport(),
		knownHostsFiles: knownHostsFiles,
		dialContext:     dialer.DialContext,
		timeout:         defaultRemoteTimeout,
	}
}

// DownloadRemote streams a remote file into destination.
func DownloadRemote(ctx context.Context, request Request, destination io.Writer) error {
	return newRemoteClient().download(ctx, request, destination)
}

// ValidateRemoteRequest verifies request syntax without starting a transfer.
func ValidateRemoteRequest(request Request) error {
	if _, err := parseRemoteTarget(request); err != nil {
		return err
	}
	if (request.Protocol == ProtocolSFTP || request.Protocol == ProtocolSCP) &&
		request.Password != "" &&
		request.Username == "" {
		return invalidRequestError("SSH username is required when a password is supplied")
	}
	return nil
}

func (c *remoteClient) download(ctx context.Context, request Request, destination io.Writer) error {
	if ctx == nil {
		return errors.New("download context is required")
	}
	if destination == nil {
		return errors.New("download destination is required")
	}

	target, err := parseRemoteTarget(request)
	if err != nil {
		return err
	}

	timeout := c.timeout
	if timeout <= 0 {
		timeout = defaultRemoteTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	switch request.Protocol {
	case ProtocolHTTP, ProtocolHTTPS:
		return c.downloadHTTP(ctx, request, target, destination)
	case ProtocolSFTP, ProtocolSCP:
		return c.downloadSSH(ctx, request, target, destination)
	default:
		return invalidRequestError("unsupported protocol")
	}
}

func parseRemoteTarget(request Request) (remoteTarget, error) {
	if request.MaxSize < 0 {
		return remoteTarget{}, invalidRequestError("size limit must not be negative")
	}
	if request.Path == "" || containsControl(request.Path) {
		return remoteTarget{}, invalidRequestError("invalid path")
	}

	switch request.Protocol {
	case ProtocolHTTP, ProtocolHTTPS:
		return parseHTTPTarget(request.Protocol, request.Path)
	case ProtocolSFTP, ProtocolSCP:
		address, remotePath, err := parseSSHTarget(request.Path)
		if err != nil {
			return remoteTarget{}, err
		}
		return remoteTarget{
			sshAddress: address,
			remotePath: remotePath,
		}, nil
	default:
		return remoteTarget{}, invalidRequestError("unsupported protocol")
	}
}

func parseHTTPTarget(protocol Protocol, path string) (remoteTarget, error) {
	parsedURL, err := url.ParseRequestURI(path)
	if err != nil || parsedURL.Opaque != "" || parsedURL.Host == "" {
		return remoteTarget{}, invalidRequestError("invalid HTTP URL")
	}

	expectedScheme := strings.ToLower(string(protocol))
	if !strings.EqualFold(parsedURL.Scheme, expectedScheme) {
		return remoteTarget{}, invalidRequestError("HTTP URL scheme does not match protocol")
	}
	if parsedURL.User != nil {
		return remoteTarget{}, invalidRequestError("HTTP URL must not contain credentials")
	}
	if parsedURL.Hostname() == "" || strings.Contains(parsedURL.Host, `\`) {
		return remoteTarget{}, invalidRequestError("invalid HTTP URL authority")
	}

	parsedURL.Scheme = expectedScheme
	return remoteTarget{httpURL: parsedURL}, nil
}

func parseSSHTarget(path string) (string, string, error) {
	var host string
	var remainder string
	bracketedHost := false

	if strings.HasPrefix(path, "[") {
		bracketedHost = true
		closingBracket := strings.IndexByte(path, ']')
		if closingBracket <= 1 || closingBracket+1 >= len(path) || path[closingBracket+1] != ':' {
			return "", "", invalidRequestError("invalid SSH path")
		}
		host = path[1:closingBracket]
		remainder = path[closingBracket+2:]
		if parsedHost, _, found := strings.Cut(host, "%"); !found {
			if net.ParseIP(host) == nil {
				return "", "", invalidRequestError("invalid SSH host")
			}
		} else if net.ParseIP(parsedHost) == nil {
			return "", "", invalidRequestError("invalid SSH host")
		}
	} else {
		separator := strings.IndexByte(path, ':')
		if separator <= 0 {
			return "", "", invalidRequestError("invalid SSH path")
		}
		host = path[:separator]
		remainder = path[separator+1:]
	}

	if host == "" || strings.TrimSpace(host) != host ||
		strings.ContainsAny(host, `/\[]`) ||
		(!bracketedHost && strings.Contains(host, ":")) ||
		containsControl(host) {
		return "", "", invalidRequestError("invalid SSH host")
	}

	port := defaultSSHPort
	remotePath := remainder
	if !validSSHRemotePath(remotePath) {
		portSeparator := strings.IndexByte(remainder, ':')
		if portSeparator <= 0 {
			return "", "", invalidRequestError("invalid SSH path")
		}

		portNumber, err := strconv.Atoi(remainder[:portSeparator])
		if err != nil || portNumber < 1 || portNumber > 65535 {
			return "", "", invalidRequestError("invalid SSH port")
		}
		port = strconv.Itoa(portNumber)
		remotePath = remainder[portSeparator+1:]
		if !validSSHRemotePath(remotePath) {
			return "", "", invalidRequestError("invalid SSH path")
		}
	}

	return net.JoinHostPort(host, port), remotePath, nil
}

func invalidRequestError(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidRequest, message)
}

func validSSHRemotePath(path string) bool {
	return strings.HasPrefix(path, "/") ||
		path == "~" ||
		strings.HasPrefix(path, "~/")
}

func containsControl(value string) bool {
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return true
		}
	}
	return false
}

func newHTTPTransport() *http.Transport {
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	return &http.Transport{
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
}

func (c *remoteClient) downloadHTTP(
	ctx context.Context,
	request Request,
	target remoteTarget,
	destination io.Writer,
) error {
	transport := c.httpTransport
	if transport == nil {
		transport = newHTTPTransport()
	}
	if idleConnectionCloser, ok := transport.(interface{ CloseIdleConnections() }); ok {
		defer idleConnectionCloser.CloseIdleConnections()
	}
	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	httpRequest, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		target.httpURL.String(),
		nil,
	)
	if err != nil {
		return errors.New("failed to create HTTP download request")
	}
	if request.Username != "" {
		httpRequest.SetBasicAuth(request.Username, request.Password)
	}

	response, err := client.Do(httpRequest)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return errors.New("HTTP download request failed")
	}
	defer response.Body.Close()

	if response.StatusCode >= 300 && response.StatusCode < 400 {
		return errors.New("HTTP redirect responses are not allowed")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("HTTP server returned status %d", response.StatusCode)
	}
	if request.MaxSize > 0 &&
		response.ContentLength >= 0 &&
		response.ContentLength > request.MaxSize {
		return sizeLimitError(request.MaxSize)
	}

	if err := copyRemoteData(ctx, destination, response.Body, request.MaxSize); err != nil {
		return err
	}
	return nil
}

func (c *remoteClient) downloadSSH(
	ctx context.Context,
	request Request,
	target remoteTarget,
	destination io.Writer,
) error {
	if request.Password != "" && request.Username == "" {
		return invalidRequestError("SSH username is required when a password is supplied")
	}

	hostKeyCallback, err := loadHostKeyCallback(c.knownHostsFiles)
	if err != nil {
		return err
	}

	var hostKeyRejected atomic.Bool
	verifiedHostKeyCallback := func(
		hostname string,
		remote net.Addr,
		key ssh.PublicKey,
	) error {
		err := hostKeyCallback(hostname, remote, key)
		if err != nil {
			hostKeyRejected.Store(true)
		}
		return err
	}

	config := &ssh.ClientConfig{
		User:            request.Username,
		HostKeyCallback: verifiedHostKeyCallback,
	}
	if request.Username != "" {
		config.Auth = []ssh.AuthMethod{ssh.Password(request.Password)}
	}

	dialContext := c.dialContext
	if dialContext == nil {
		dialer := &net.Dialer{}
		dialContext = dialer.DialContext
	}
	connection, err := dialContext(ctx, "tcp", target.sshAddress)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return errors.New("SSH connection failed")
	}

	if deadline, ok := ctx.Deadline(); ok {
		if err := connection.SetDeadline(deadline); err != nil {
			connection.Close()
			return errors.New("failed to set SSH transfer deadline")
		}
	}
	stopContextWatch := closeOnContextDone(ctx, connection)
	defer stopContextWatch()

	clientConnection, channels, requests, err := ssh.NewClientConn(
		connection,
		target.sshAddress,
		config,
	)
	if err != nil {
		connection.Close()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if hostKeyRejected.Load() {
			return errors.New("SSH host-key verification failed")
		}
		return errors.New("SSH connection or authentication failed")
	}

	client := ssh.NewClient(clientConnection, channels, requests)
	defer client.Close()

	switch request.Protocol {
	case ProtocolSFTP:
		return downloadSFTP(ctx, client, target.remotePath, destination, request.MaxSize)
	case ProtocolSCP:
		return downloadSCP(ctx, client, target.remotePath, destination, request.MaxSize)
	default:
		return errors.New("unsupported SSH download protocol")
	}
}

func loadHostKeyCallback(paths []string) (ssh.HostKeyCallback, error) {
	existingPaths := make([]string, 0, len(paths))
	for _, path := range paths {
		info, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !info.Mode().IsRegular() {
			return nil, errors.New("SSH known-host data is unreadable")
		}
		existingPaths = append(existingPaths, path)
	}

	callback, err := knownhosts.New(existingPaths...)
	if err != nil {
		return nil, errors.New("SSH known-host data is invalid or unreadable")
	}
	return callback, nil
}

func closeOnContextDone(ctx context.Context, closer io.Closer) func() {
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			closer.Close()
		case <-done:
		}
	}()
	return func() {
		close(done)
	}
}

func downloadSFTP(
	ctx context.Context,
	client *ssh.Client,
	remotePath string,
	destination io.Writer,
	maxSize int64,
) error {
	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return errors.New("failed to start SFTP transfer")
	}
	defer sftpClient.Close()

	resolvedRemotePath, err := resolveSFTPRemotePath(remotePath, sftpClient.Getwd)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return err
	}
	remoteFile, err := sftpClient.Open(resolvedRemotePath)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return errors.New("failed to open remote SFTP file")
	}
	defer remoteFile.Close()

	if maxSize > 0 {
		info, err := remoteFile.Stat()
		if err != nil {
			return errors.New("failed to inspect remote SFTP file")
		}
		if info.Size() > maxSize {
			return sizeLimitError(maxSize)
		}
	}

	return copyRemoteData(ctx, destination, remoteFile, maxSize)
}

func downloadSCP(
	ctx context.Context,
	client *ssh.Client,
	remotePath string,
	destination io.Writer,
	maxSize int64,
) error {
	command := "scp -f -- " + scpRemoteArgument(remotePath)
	session, err := client.NewSession()
	if err != nil {
		return errors.New("failed to start SCP session")
	}
	defer session.Close()

	stdin, err := session.StdinPipe()
	if err != nil {
		return errors.New("failed to start SCP input stream")
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		return errors.New("failed to start SCP output stream")
	}
	session.Stderr = io.Discard

	if err := session.Start(command); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return errors.New("failed to start SCP transfer")
	}
	if err := writeSCPAck(stdin); err != nil {
		return errors.New("failed to acknowledge SCP transfer")
	}

	reader := bufio.NewReaderSize(stdout, maxSCPControlLine)
	for {
		recordType, err := reader.ReadByte()
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			return errors.New("invalid SCP response")
		}

		switch recordType {
		case 1, 2:
			_, _ = readSCPControlLine(reader)
			return errors.New("SCP server rejected download")
		case 'T':
			if _, err := readSCPControlLine(reader); err != nil {
				return err
			}
			if err := writeSCPAck(stdin); err != nil {
				return errors.New("failed to acknowledge SCP metadata")
			}
		case 'C':
			controlLine, err := readSCPControlLine(reader)
			if err != nil {
				return err
			}
			size, err := parseSCPFileControl(controlLine)
			if err != nil {
				return err
			}
			if maxSize > 0 && size > maxSize {
				return sizeLimitError(maxSize)
			}
			if err := writeSCPAck(stdin); err != nil {
				return errors.New("failed to acknowledge SCP file")
			}
			if err := copyExactRemoteData(ctx, destination, reader, size); err != nil {
				return err
			}

			status, err := reader.ReadByte()
			if err != nil {
				return errors.New("invalid SCP file completion response")
			}
			if status != 0 {
				if status == 1 || status == 2 {
					_, _ = readSCPControlLine(reader)
					return errors.New("SCP server reported a transfer failure")
				}
				return errors.New("invalid SCP file completion response")
			}
			if err := writeSCPAck(stdin); err != nil {
				return errors.New("failed to acknowledge SCP file completion")
			}
			if err := stdin.Close(); err != nil {
				return errors.New("failed to close SCP input stream")
			}
			if err := session.Wait(); err != nil {
				if ctxErr := ctx.Err(); ctxErr != nil {
					return ctxErr
				}
				return errors.New("SCP transfer failed")
			}
			return nil
		case 'D', 'E':
			return errors.New("SCP directories are not supported")
		default:
			return errors.New("invalid SCP response")
		}
	}
}

func resolveSFTPRemotePath(
	remotePath string,
	getWorkingDirectory func() (string, error),
) (string, error) {
	if remotePath != "~" && !strings.HasPrefix(remotePath, "~/") {
		return remotePath, nil
	}

	workingDirectory, err := getWorkingDirectory()
	if err != nil || !strings.HasPrefix(workingDirectory, "/") {
		return "", errors.New("failed to resolve remote SFTP home")
	}
	if remotePath == "~" || remotePath == "~/" {
		return workingDirectory, nil
	}
	suffix := strings.TrimPrefix(remotePath, "~/")
	if workingDirectory == "/" {
		return "/" + suffix, nil
	}
	return strings.TrimSuffix(workingDirectory, "/") + "/" + suffix, nil
}

func scpRemoteArgument(remotePath string) string {
	if remotePath == "~" {
		return `"$HOME"`
	}
	if strings.HasPrefix(remotePath, "~/") {
		suffix := strings.TrimPrefix(remotePath, "~/")
		if suffix == "" {
			return `"$HOME"/`
		}
		return `"$HOME"/` + shellQuote(suffix)
	}
	return shellQuote(remotePath)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func writeSCPAck(writer io.Writer) error {
	_, err := writer.Write([]byte{0})
	return err
}

func readSCPControlLine(reader *bufio.Reader) (string, error) {
	line := make([]byte, 0, 128)
	for len(line) <= maxSCPControlLine {
		character, err := reader.ReadByte()
		if err != nil {
			return "", errors.New("invalid SCP control response")
		}
		if character == '\n' {
			return string(line), nil
		}
		line = append(line, character)
	}
	return "", errors.New("SCP control response exceeds size limit")
}

func parseSCPFileControl(controlLine string) (int64, error) {
	fields := strings.SplitN(controlLine, " ", 3)
	if len(fields) != 3 || len(fields[0]) != 4 || fields[2] == "" {
		return 0, errors.New("invalid SCP file response")
	}
	if _, err := strconv.ParseUint(fields[0], 8, 32); err != nil {
		return 0, errors.New("invalid SCP file response")
	}
	size, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil || size < 0 {
		return 0, errors.New("invalid SCP file response")
	}
	return size, nil
}

func copyRemoteData(
	ctx context.Context,
	destination io.Writer,
	source io.Reader,
	maxSize int64,
) error {
	buffer := make([]byte, 32*1024)
	var total int64
	emptyReads := 0

	for {
		count, readErr := source.Read(buffer)
		if count > 0 {
			emptyReads = 0
			total += int64(count)
			if maxSize > 0 && total > maxSize {
				return sizeLimitError(maxSize)
			}
			if err := writeAll(destination, buffer[:count]); err != nil {
				return fmt.Errorf("failed to write downloaded data: %w", err)
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			return errors.New("remote transfer failed")
		}
		if count == 0 {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			emptyReads++
			if emptyReads >= 100 {
				return errors.New("remote transfer made no progress")
			}
		}
	}
}

func copyExactRemoteData(
	ctx context.Context,
	destination io.Writer,
	source io.Reader,
	size int64,
) error {
	buffer := make([]byte, 32*1024)
	remaining := size

	for remaining > 0 {
		readSize := int64(len(buffer))
		if remaining < readSize {
			readSize = remaining
		}
		count, err := io.ReadFull(source, buffer[:readSize])
		if count > 0 {
			if writeErr := writeAll(destination, buffer[:count]); writeErr != nil {
				return fmt.Errorf("failed to write downloaded data: %w", writeErr)
			}
			remaining -= int64(count)
		}
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			return errors.New("remote transfer ended before the declared size")
		}
	}
	return nil
}

func writeAll(destination io.Writer, data []byte) error {
	for len(data) > 0 {
		count, err := destination.Write(data)
		if err != nil {
			return err
		}
		if count == 0 {
			return io.ErrShortWrite
		}
		data = data[count:]
	}
	return nil
}

func sizeLimitError(maxSize int64) error {
	return fmt.Errorf("remote download exceeds size limit of %d bytes", maxSize)
}
