package domain

import "testing"

func TestExecutionCatalogRejectsInvalidRelationshipsAndBounds(t *testing.T) {
	valid := func() ExecutionCatalog {
		return ExecutionCatalog{Version: 1, Runtimes: []CatalogRuntime{{ID: "agent", DisplayName: "Local Agent"}},
			Workspaces: []CatalogWorkspace{{ID: "project", DisplayName: "My project", RuntimeIDs: []string{"agent"}}}}
	}
	for name, mutate := range map[string]func(*ExecutionCatalog){
		"version":               func(c *ExecutionCatalog) { c.Version = 2 },
		"unknown runtime":       func(c *ExecutionCatalog) { c.Workspaces[0].RuntimeIDs = []string{"unknown"} },
		"duplicate runtime":     func(c *ExecutionCatalog) { c.Runtimes = append(c.Runtimes, c.Runtimes[0]) },
		"duplicate association": func(c *ExecutionCatalog) { c.Workspaces[0].RuntimeIDs = []string{"agent", "agent"} },
		"path as ID":            func(c *ExecutionCatalog) { c.Workspaces[0].ID = "/etc/project" },
		"control character":     func(c *ExecutionCatalog) { c.Runtimes[0].DisplayName = "Agent\nsecret" },
		"oversized":             func(c *ExecutionCatalog) { c.Workspaces = make([]CatalogWorkspace, MaxCatalogEntries+1) },
	} {
		t.Run(name, func(t *testing.T) {
			catalog := valid()
			mutate(&catalog)
			if err := catalog.Validate(); err == nil {
				t.Fatal("invalid catalog accepted")
			}
		})
	}
	catalog := valid()
	if err := catalog.Validate(); err != nil {
		t.Fatal(err)
	}
	if !catalog.Allows("project", "agent") || catalog.Allows("other", "agent") {
		t.Fatal("workspace association was not enforced")
	}
	before, _ := catalog.Revision()
	catalog.Workspaces[0].DisplayName = "Renamed project"
	after, _ := catalog.Revision()
	if before == after {
		t.Fatal("catalog content change retained revision")
	}
}
