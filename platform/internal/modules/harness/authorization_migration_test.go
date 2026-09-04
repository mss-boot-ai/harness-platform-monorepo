package harness

import (
	"errors"
	"testing"

	"github.com/mss-boot-io/mss-boot-admin/admin/models"
	adminpkg "github.com/mss-boot-io/mss-boot-admin/admin/pkg"
	migrationmodels "github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/migration/models"
	"gorm.io/gorm"
)

func TestHarnessAuthorizationMigrationProjectsExactPolicyAndIsRepeatable(t *testing.T) {
	db := openAuthorizationTestDB(t, true)
	for attempt := 1; attempt <= 2; attempt++ {
		if err := applyHarnessAuthorizationMigration(db, HarnessAuthorizationMigrationID.String()); err != nil {
			t.Fatalf("authorization migration attempt %d: %v", attempt, err)
		}
	}

	var versionCount int64
	if err := db.Model(new(migrationmodels.Migration)).Where(
		"version = ?", HarnessAuthorizationMigrationID.String(),
	).Count(&versionCount).Error; err != nil || versionCount != 1 {
		t.Fatalf("authorization migration version count = %d, err=%v", versionCount, err)
	}
	var role models.Role
	if err := db.Where("name = ?", harnessDefaultRoleName).Take(&role).Error; err != nil {
		t.Fatalf("load default Harness role: %v", err)
	}
	var menu models.Menu
	if err := db.Where("type = ? AND path = ?", adminpkg.MenuAccessType, "/harness").Take(&menu).Error; err != nil {
		t.Fatalf("load Harness menu: %v", err)
	}
	if menu.Permission != PermissionRead || menu.Icon != "RobotOutlined" || menu.Sort != 45 || menu.HideInMenu {
		t.Fatalf("Harness menu metadata = %#v", menu)
	}

	components := make(map[string]models.Menu, len(harnessPermissionSeeds))
	for _, seed := range harnessPermissionSeeds {
		var component models.Menu
		if err := db.Where(
			"type = ? AND path = ? AND permission = ?",
			adminpkg.ComponentAccessType, seed.componentPath, seed.code,
		).Take(&component).Error; err != nil {
			t.Fatalf("load permission component %s: %v", seed.code, err)
		}
		if component.ParentID != menu.ID || !component.HideInMenu {
			t.Fatalf("permission component %s = %#v", seed.code, component)
		}
		components[seed.code] = component
		assertHarnessAuthorizationPolicy(t, db, role.ID, adminpkg.ComponentAccessType, component.Path, component.Method)
	}
	assertHarnessAuthorizationPolicy(t, db, role.ID, adminpkg.MenuAccessType, menu.Path, menu.Method)

	for _, route := range harnessAuthorizationRoutes {
		component := components[route.permission]
		var api models.Menu
		if err := db.Where(
			"type = ? AND path = ? AND method = ?",
			adminpkg.APIAccessType, route.path, route.method,
		).Take(&api).Error; err != nil {
			t.Fatalf("load API authorization %s %s: %v", route.method, route.path, err)
		}
		if api.ParentID != component.ID || api.Permission != route.permission || !api.HideInMenu {
			t.Fatalf("API authorization %s %s = %#v", route.method, route.path, api)
		}
		assertHarnessAuthorizationPolicy(t, db, role.ID, adminpkg.APIAccessType, route.path, route.method)
	}

	var revisions []models.ConfigRevision
	if err := db.Where("resource = ?", harnessAuthorizationResource).Order("scope, owner_id").Find(&revisions).Error; err != nil {
		t.Fatalf("load authorization revisions: %v", err)
	}
	if len(revisions) != 2 {
		t.Fatalf("authorization revisions = %d, want 2", len(revisions))
	}
	for _, revision := range revisions {
		if revision.Revision != 1 {
			t.Fatalf("authorization revision = %#v, want revision 1", revision)
		}
	}
	if err := verifyHarnessAuthorizationReadiness(db); err != nil {
		t.Fatalf("verifyHarnessAuthorizationReadiness: %v", err)
	}
}

func TestHarnessAuthorizationMigrationRollsBackOnFailure(t *testing.T) {
	db := openAuthorizationTestDB(t, true)
	injected := errors.New("injected authorization failure")
	err := applyHarnessAuthorizationMigrationWithHook(
		db,
		HarnessAuthorizationMigrationID.String(),
		func() error { return injected },
	)
	if !errors.Is(err, injected) {
		t.Fatalf("authorization migration error = %v, want injected failure", err)
	}
	for name, model := range map[string]any{
		"roles":     new(models.Role),
		"menus":     new(models.Menu),
		"policies":  new(models.CasbinRule),
		"revisions": new(models.ConfigRevision),
		"versions":  new(migrationmodels.Migration),
	} {
		var count int64
		if err := db.Model(model).Count(&count).Error; err != nil {
			t.Fatalf("count %s after rollback: %v", name, err)
		}
		if count != 0 {
			t.Fatalf("%s after rollback = %d, want zero", name, count)
		}
	}
}

func TestHarnessAuthorizationMigrationAndReadinessFailClosedOnMissingPolicyTable(t *testing.T) {
	db := openAuthorizationTestDB(t, false)
	if err := applyHarnessAuthorizationMigration(db, HarnessAuthorizationMigrationID.String()); err == nil {
		t.Fatal("authorization migration accepted a missing Admin policy table")
	}
	if err := verifyHarnessAuthorizationReadiness(db); err == nil {
		t.Fatal("authorization readiness accepted a missing Admin policy table")
	}
}

func assertHarnessAuthorizationPolicy(
	t *testing.T,
	db *gorm.DB,
	roleID string,
	accessType adminpkg.AccessType,
	path string,
	method string,
) {
	t.Helper()
	var count int64
	if err := db.Model(new(models.CasbinRule)).Where(
		"ptype = ? AND v0 = ? AND v1 = ? AND v2 = ? AND v3 = ?",
		"p", roleID, accessType.String(), path, method,
	).Count(&count).Error; err != nil {
		t.Fatalf("count policy %s %s: %v", method, path, err)
	}
	if count != 1 {
		t.Fatalf("policy %s %s count = %d, want 1", method, path, count)
	}
}
