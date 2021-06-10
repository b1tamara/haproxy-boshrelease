package acceptance_tests

import (
	"fmt"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
)

var _ = Describe("HTTP Health Check", func() {
	opsfileHTTPHealthcheck := `---
# Enable health check
- type: replace
  path: /instance_groups/name=haproxy/jobs/name=haproxy/properties/ha_proxy/enable_health_check_http?
  value: true
`

	It("Correctly fails to start if there is no healthy backend", func() {
		deploymentName := "haproxy"
		haproxyBackendPort := 12000
		// Expect initial deployment to be failing due to lack of healthy backends
		haproxyInfo, _ := deployHAProxy(baseManifestVars{
			haproxyBackendPort:    haproxyBackendPort,
			haproxyBackendServers: []string{"127.0.0.1"},
			deploymentName:        deploymentName,
		}, []string{opsfileHTTPHealthcheck}, map[string]interface{}{}, false)
		defer deleteDeployment(deploymentName)

		// Verify that is in a failing state
		Expect(boshInstances(deploymentName)[0].ProcessState).To(Or(Equal("failing"), Equal("unresponsive agent")))

		closeLocalServer, localPort := startDefaultTestServer()
		defer closeLocalServer()

		closeTunnel := setupTunnelFromHaproxyToTestServer(haproxyInfo, haproxyBackendPort, localPort)
		defer closeTunnel()

		By("Waiting monit to report HAProxy is now healthy (due to having a healthy backend instance)")
		// Since the backend is now listening, HAProxy healthcheck should start returning healthy
		// and monit should in turn start reporting a healthy process
		// We will up to wait one minute for the status to stabilise
		Eventually(func() string {
			return boshInstances(deploymentName)[0].ProcessState
		}, time.Minute, time.Second).Should(Equal("running"))

		By("The healthcheck health endpoint should report a 200 status code")
		resp, err := http.Get(fmt.Sprintf("http://%s:8080/health", haproxyInfo.PublicIP))
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusOK))

		By("Sending a request to HAProxy works")
		resp, err = http.Get(fmt.Sprintf("http://%s", haproxyInfo.PublicIP))
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		Eventually(gbytes.BufferReader(resp.Body)).Should(gbytes.Say("Hello cloud foundry"))

	})

	It("Correctly starts if there is a healthy backend", func() {
		backendDeploymentName := "haproxy-backend"
		// For this test we will use a second HAProxy as pre-existing healthy 'backend'
		haproxyBackendPort := 12000
		backendHaproxyInfo, _ := deployHAProxy(baseManifestVars{
			haproxyBackendPort:    haproxyBackendPort,
			haproxyBackendServers: []string{"127.0.0.1"},
			deploymentName:        backendDeploymentName,
		}, []string{}, map[string]interface{}{}, true)
		defer deleteDeployment(backendDeploymentName)

		closeLocalServer, backendLocalPort := startDefaultTestServer()
		defer closeLocalServer()

		closeTunnel := setupTunnelFromHaproxyToTestServer(backendHaproxyInfo, haproxyBackendPort, backendLocalPort)
		defer closeTunnel()

		// Now deploy test HAProxy with 'haproxy-backend' configured as backend
		deploymentName := "haproxy"
		haproxyInfo, _ := deployHAProxy(baseManifestVars{
			haproxyBackendPort:    80,
			haproxyBackendServers: []string{backendHaproxyInfo.PublicIP},
			deploymentName:        deploymentName,
		}, []string{opsfileHTTPHealthcheck}, map[string]interface{}{}, true)
		defer deleteDeployment(deploymentName)

		// Verify that instance is in a running state
		Expect(boshInstances(deploymentName)[0].ProcessState).To(Equal("running"))

		By("The healthcheck health endpoint should report a 200 status code")
		resp, err := http.Get(fmt.Sprintf("http://%s:8080/health", haproxyInfo.PublicIP))
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusOK))

		By("Sending a request to HAProxy works")
		resp, err = http.Get(fmt.Sprintf("http://%s", haproxyInfo.PublicIP))
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		Eventually(gbytes.BufferReader(resp.Body)).Should(gbytes.Say("Hello cloud foundry"))
	})
})