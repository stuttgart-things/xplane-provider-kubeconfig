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

package decrypt

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/getsops/sops/v3/decrypt"
	"github.com/pkg/errors"
)

// SOPSDecrypt decrypts SOPS-encrypted data using the provided age key.
// It detects the format from the file path extension.
func SOPSDecrypt(data []byte, filePath string, ageKey string) ([]byte, error) {
	if ageKey == "" {
		return nil, errors.New("age key is empty")
	}

	// Set the age key for SOPS to pick up
	prev := os.Getenv("SOPS_AGE_KEY")
	os.Setenv("SOPS_AGE_KEY", ageKey)
	defer os.Setenv("SOPS_AGE_KEY", prev)

	format := formatFromPath(filePath)
	cleartext, err := decrypt.Data(data, format)
	if err != nil {
		return nil, errors.Wrap(err, "cannot decrypt SOPS data")
	}

	return cleartext, nil
}

// formatFromPath returns the SOPS format string based on file extension.
func formatFromPath(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".json":
		return "json"
	case ".env", ".ini":
		return "dotenv"
	case ".yml", ".yaml":
		return "yaml"
	default:
		return "binary"
	}
}
