package notifier

import (
	"net/http"

	"github.com/stretchr/testify/mock"
)

// MockHTTPClient is a testify/mock implementation of the HTTPClient interface
// for use in unit tests.
type MockHTTPClient struct {
	mock.Mock
}

// Ensure MockHTTPClient satisfies the HTTPClient interface at compile time.
var _ HTTPClient = (*MockHTTPClient)(nil)

// Do mocks the Do method of HTTPClient.
func (m *MockHTTPClient) Do(req *http.Request) (*http.Response, error) {
	args := m.Called(req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*http.Response), args.Error(1)
}
