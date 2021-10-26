package acceptance_tests

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
)

// This deployment is reused between tests to speed up test execution
var defaultDeploymentName = "haproxy"

func TestAcceptanceTests(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "AcceptanceTests Suite")
}

var _ = BeforeSuite(func() {
	var err error
	config, err = loadConfig()
	Expect(err).NotTo(HaveOccurred())
})

var _ = AfterSuite(func() {
	deleteDeployment(defaultDeploymentName)
})

// Starts a simple test server that returns 200 OK or echoes websocket messages back
func startDefaultTestServer() (func(), int) {
	var upgrader = websocket.Upgrader{}

	By("Starting a local websocket server to act as a backend")
	closeLocalServer, localPort, err := startLocalHTTPServer(nil, func(w http.ResponseWriter, r *http.Request) {
		// if no upgrade requested, act like a normal HTTP server
		if strings.ToLower(r.Header.Get("Upgrade")) != "websocket" {
			fmt.Fprintln(w, "Hello cloud foundry")
			return
		}

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		defer conn.Close()

		for {
			messageType, message, err := conn.ReadMessage()
			if err != nil {
				break
			}
			err = conn.WriteMessage(messageType, message)
			if err != nil {
				break
			}
		}
	})

	Expect(err).NotTo(HaveOccurred())
	return closeLocalServer, localPort
}

// Sets up SSH tunnel from HAProxy VM to test server
func setupTunnelFromHaproxyToTestServer(haproxyInfo haproxyInfo, haproxyBackendPort, localPort int) func() {
	By(fmt.Sprintf("Creating a reverse SSH tunnel from HAProxy backend (port %d) to local HTTP server (port %d)", haproxyBackendPort, localPort))
	ctx, cancelFunc := context.WithCancel(context.Background())
	err := startReverseSSHPortForwarder(haproxyInfo.SSHUser, haproxyInfo.PublicIP, haproxyInfo.SSHPrivateKey, haproxyBackendPort, localPort, ctx)
	Expect(err).NotTo(HaveOccurred())

	By("Waiting a few seconds so that HAProxy can detect the backend server is listening")
	// HAProxy backend health check interval is 1 second
	// So we wait five seconds here to ensure that HAProxy
	// has time to verify that the backend is now up
	time.Sleep(5 * time.Second)

	return cancelFunc
}

// Sets up SSH tunnel from local machine to HAProxy
func setupTunnelFromLocalMachineToHAProxy(haproxyInfo haproxyInfo, localPort, haproxyPort int) func() {
	By(fmt.Sprintf("Creating a SSH tunnel from localmachine (port %d) to HAProxy (port %d)", localPort, haproxyPort))
	ctx, cancelFunc := context.WithCancel(context.Background())
	err := startSSHPortForwarder(haproxyInfo.SSHUser, haproxyInfo.PublicIP, haproxyInfo.SSHPrivateKey, localPort, haproxyPort, ctx)
	Expect(err).NotTo(HaveOccurred())

	return cancelFunc
}

func expectTestServer200(resp *http.Response, err error) {
	Expect(err).NotTo(HaveOccurred())
	Expect(resp.StatusCode).To(Equal(http.StatusOK))
	Eventually(gbytes.BufferReader(resp.Body)).Should(gbytes.Say("Hello cloud foundry"))
}

func expect200(resp *http.Response, err error) {
	Expect(err).NotTo(HaveOccurred())
	Expect(resp.StatusCode).To(Equal(http.StatusOK))
}

func expect400(resp *http.Response, err error) {
	Expect(err).NotTo(HaveOccurred())
	Expect(resp.StatusCode).To(Equal(http.StatusBadRequest))
}

func expect421(resp *http.Response, err error) {
	Expect(err).NotTo(HaveOccurred())
	Expect(resp.StatusCode).To(Equal(http.StatusMisdirectedRequest))
}

func expectTLSUnknownCertificateAuthorityErr(err error) {
	checkTLSErr(err, "tls: unknown certificate authority")
}

func expectTLSHandshakeFailureErr(err error) {
	checkTLSErr(err, "tls: handshake failure")
}

func expectTLSCertificateRequiredErr(err error) {
	checkTLSErr(err, "tls: certificate required")
}

func expectTLSUnrecognizedNameErr(err error) {
	checkTLSErr(err, "tls: unrecognized name")
}

func checkTLSErr(err error, expectString string) {
	Expect(err).To(HaveOccurred())
	urlErr, ok := err.(*url.Error)
	Expect(ok).To(BeTrue())
	tlsErr := urlErr.Unwrap()
	var opErr *net.OpError
	Expect(errors.As(tlsErr, &opErr)).To(BeTrue())
	Expect(opErr.Err.Error()).To(ContainSubstring(expectString))
}
