/*
Copyright 2025 The Crossplane Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package vault provides a client for reading kubeconfigs from Vault KVv2.
package vault

import (
	"context"
	"fmt"
	"os"

	"github.com/hashicorp/vault/api"
	"github.com/pkg/errors"
)

const (
	defaultSATokenPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"
)

// Client wraps the Vault API for KVv2 operations.
type Client struct {
	client    *api.Client
	mountPath string
}

// New creates and authenticates a Vault client.
func New(ctx context.Context, address, namespace, mountPath string, authFn func(*api.Client) error) (*Client, error) {
	cfg := api.DefaultConfig()
	cfg.Address = address

	c, err := api.NewClient(cfg)
	if err != nil {
		return nil, errors.Wrap(err, "cannot create Vault client")
	}

	if namespace != "" {
		c.SetNamespace(namespace)
	}

	if err := authFn(c); err != nil {
		return nil, errors.Wrap(err, "cannot authenticate to Vault")
	}

	if mountPath == "" {
		mountPath = "secret"
	}

	return &Client{client: c, mountPath: mountPath}, nil
}

// ReadKVv2 reads a KVv2 secret and returns the data map and metadata version.
func (c *Client) ReadKVv2(ctx context.Context, path string) (map[string]interface{}, int, error) {
	kv := c.client.KVv2(c.mountPath)
	secret, err := kv.Get(ctx, path)
	if err != nil {
		return nil, 0, errors.Wrapf(err, "cannot read Vault KVv2 secret at %q", path)
	}
	if secret == nil || secret.Data == nil {
		return nil, 0, fmt.Errorf("vault KVv2 secret at %q is empty", path)
	}

	version := 0
	if secret.VersionMetadata != nil {
		version = secret.VersionMetadata.Version
	}

	return secret.Data, version, nil
}

// AuthKubernetes returns an auth function for Kubernetes JWT auth.
func AuthKubernetes(role, mountPath string) func(*api.Client) error {
	if mountPath == "" {
		mountPath = "kubernetes"
	}
	return func(c *api.Client) error {
		jwt, err := os.ReadFile(defaultSATokenPath)
		if err != nil {
			return errors.Wrap(err, "cannot read service account token")
		}

		loginPath := fmt.Sprintf("auth/%s/login", mountPath)
		secret, err := c.Logical().Write(loginPath, map[string]interface{}{
			"role": role,
			"jwt":  string(jwt),
		})
		if err != nil {
			return errors.Wrap(err, "Vault Kubernetes auth login failed")
		}
		if secret == nil || secret.Auth == nil {
			return fmt.Errorf("vault Kubernetes auth returned no token")
		}

		c.SetToken(secret.Auth.ClientToken)
		return nil
	}
}

// AuthAppRole returns an auth function for AppRole auth.
func AuthAppRole(roleID, secretID, mountPath string) func(*api.Client) error {
	if mountPath == "" {
		mountPath = "approle"
	}
	return func(c *api.Client) error {
		loginPath := fmt.Sprintf("auth/%s/login", mountPath)
		secret, err := c.Logical().Write(loginPath, map[string]interface{}{
			"role_id":   roleID,
			"secret_id": secretID,
		})
		if err != nil {
			return errors.Wrap(err, "Vault AppRole auth login failed")
		}
		if secret == nil || secret.Auth == nil {
			return fmt.Errorf("vault AppRole auth returned no token")
		}

		c.SetToken(secret.Auth.ClientToken)
		return nil
	}
}
