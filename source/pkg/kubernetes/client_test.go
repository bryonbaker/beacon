package kubernetes

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestNewClients_NoClusterOrKubeconfig_ReturnsError(t *testing.T) {
	// Ensure we are not running in-cluster and KUBECONFIG points nowhere.
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("KUBERNETES_SERVICE_PORT", "")
	t.Setenv("KUBECONFIG", "/tmp/nonexistent-kubeconfig-file")

	// Override HOME so the fallback path also fails.
	t.Setenv("HOME", "/tmp/nonexistent-home")

	logger := zap.NewNop()

	typed, dyn, err := NewClients(logger)

	require.Error(t, err, "NewClients should return an error when no cluster or kubeconfig is available")
	assert.Nil(t, typed, "typed client should be nil on error")
	assert.Nil(t, dyn, "dynamic client should be nil on error")
	assert.Contains(t, err.Error(), "failed to build config", "error message should indicate config failure")
}

func TestNewClients_InvalidKubeconfig_ReturnsError(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("KUBERNETES_SERVICE_PORT", "")

	// Create a temporary file with invalid kubeconfig content.
	tmpFile, err := os.CreateTemp("", "bad-kubeconfig-*.yaml")
	require.NoError(t, err)
	defer os.Remove(tmpFile.Name())

	_, err = tmpFile.WriteString("this is not valid kubeconfig")
	require.NoError(t, err)
	require.NoError(t, tmpFile.Close())

	t.Setenv("KUBECONFIG", tmpFile.Name())

	logger := zap.NewNop()

	typed, dyn, err := NewClients(logger)

	require.Error(t, err, "NewClients should return an error for invalid kubeconfig")
	assert.Nil(t, typed, "typed client should be nil on error")
	assert.Nil(t, dyn, "dynamic client should be nil on error")
}
