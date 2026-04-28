// export_test.go exposes internal constructors for use in package-level tests.
// It is compiled only when running tests.
package docker

// NewTestClient creates a Client backed by the provided sdkAPI implementation.
// It is intended for use in unit tests that need to inject a fake SDK.
func NewTestClient(sdk sdkAPI) *Client {
	return newClientWithSDK(sdk)
}
