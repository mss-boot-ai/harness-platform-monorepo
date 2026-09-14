package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
	"unicode"
)

const MaxCatalogEntries = 64

// ExecutionCatalog is the public projection of local, owner-approved profiles.
// Paths, command lines, environment and arbitrary capability blobs have no field here.
type ExecutionCatalog struct {
	Version    uint32             `json:"version"`
	Runtimes   []CatalogRuntime   `json:"runtimes"`
	Workspaces []CatalogWorkspace `json:"workspaces"`
}

type CatalogRuntime struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
}

type CatalogWorkspace struct {
	ID          string   `json:"id"`
	DisplayName string   `json:"displayName"`
	RuntimeIDs  []string `json:"runtimeIds"`
}

type PublishedCatalog struct {
	EndpointID           ID
	OwnerUserID          string
	TenantID             string
	ConnectionGeneration uint64
	Revision             string
	Catalog              ExecutionCatalog
	PublishedAt          time.Time
	ExpiresAt            time.Time
}

func (catalog ExecutionCatalog) Validate() error {
	if catalog.Version != 1 || len(catalog.Runtimes) > MaxCatalogEntries || len(catalog.Workspaces) > MaxCatalogEntries {
		return NewProblem(CodeInvalidArgument, "execution catalog version or size is invalid", nil)
	}
	runtimes := make(map[string]bool, len(catalog.Runtimes))
	for _, runtime := range catalog.Runtimes {
		if !catalogID(runtime.ID) || !catalogName(runtime.DisplayName) || runtimes[runtime.ID] {
			return NewProblem(CodeInvalidArgument, "execution catalog runtime is invalid", nil)
		}
		runtimes[runtime.ID] = true
	}
	workspaces := make(map[string]bool, len(catalog.Workspaces))
	for _, workspace := range catalog.Workspaces {
		if !catalogID(workspace.ID) || !catalogName(workspace.DisplayName) || workspaces[workspace.ID] || len(workspace.RuntimeIDs) == 0 || len(workspace.RuntimeIDs) > MaxCatalogEntries {
			return NewProblem(CodeInvalidArgument, "execution catalog workspace is invalid", nil)
		}
		workspaces[workspace.ID] = true
		seen := make(map[string]bool, len(workspace.RuntimeIDs))
		for _, id := range workspace.RuntimeIDs {
			if !runtimes[id] || seen[id] {
				return NewProblem(CodeInvalidArgument, "execution catalog workspace runtime is invalid", nil)
			}
			seen[id] = true
		}
	}
	return nil
}

func (catalog ExecutionCatalog) Revision() (string, error) {
	if err := catalog.Validate(); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(catalog)
	if err != nil {
		return "", err
	}
	if len(encoded) > 12*1024 {
		return "", NewProblem(CodeInvalidArgument, "execution catalog exceeds its byte limit", nil)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func (catalog ExecutionCatalog) Allows(workspaceID, runtimeID string) bool {
	for _, workspace := range catalog.Workspaces {
		if workspace.ID == workspaceID {
			for _, id := range workspace.RuntimeIDs {
				if id == runtimeID {
					return true
				}
			}
		}
	}
	return false
}

func catalogID(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	return true
}

func catalogName(value string) bool {
	if strings.TrimSpace(value) != value || len(value) == 0 || len(value) > 128 {
		return false
	}
	for _, c := range value {
		if unicode.IsControl(c) {
			return false
		}
	}
	return true
}
