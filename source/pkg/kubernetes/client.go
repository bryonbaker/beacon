// Package kubernetes provides Kubernetes client construction utilities.
package kubernetes

import (
	"fmt"
	"os"
	"path/filepath"

	"go.uber.org/zap"
	"k8s.io/client-go/dynamic"
	k8s "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// NewClients creates both a typed and dynamic Kubernetes client.
// It tries in-cluster config first, then falls back to kubeconfig.
func NewClients(logger *zap.Logger) (k8s.Interface, dynamic.Interface, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		logger.Info("not running in-cluster, trying kubeconfig")
		kubeconfig := os.Getenv("KUBECONFIG")
		if kubeconfig == "" {
			home, _ := os.UserHomeDir()
			kubeconfig = filepath.Join(home, ".kube", "config")
		}
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to build config: %w", err)
		}
	}

	typed, err := k8s.NewForConfig(config)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create typed client: %w", err)
	}

	dyn, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}

	return typed, dyn, nil
}
