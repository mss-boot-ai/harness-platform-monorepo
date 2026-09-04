package harness

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/store"
	"github.com/mss-boot-io/mss-boot-admin/admin/models"
	adminpkg "github.com/mss-boot-io/mss-boot-admin/admin/pkg"
	"github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/enum"
	"github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/migration"
	migrationmodels "github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/migration/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const HarnessAuthorizationMigrationID migration.MigrationID = "20260904030000"

const (
	harnessAuthorizationScopeRole   = "role"
	harnessAuthorizationScopeGlobal = "global"
	harnessAuthorizationResource    = "authorization"
	harnessDefaultRoleName          = "admin"
)

type harnessPermissionSeed struct {
	code          string
	displayName   string
	componentPath string
}

var harnessPermissionSeeds = []harnessPermissionSeed{
	{code: PermissionRead, displayName: "查看 Harness 状态", componentPath: "/harness/permissions/read"},
	{code: PermissionOperate, displayName: "操作 Harness 会话", componentPath: "/harness/permissions/operate"},
	{code: PermissionApprove, displayName: "审批 Harness 注册", componentPath: "/harness/permissions/approve"},
	{code: PermissionRevoke, displayName: "暂停或吊销 Harness 端点", componentPath: "/harness/permissions/revoke"},
}

func registerHarnessMigrations(runner *migration.Migration) error {
	if runner == nil {
		return errors.New("harness migration runner is required")
	}
	if err := store.RegisterAllMigrations(runner); err != nil {
		return err
	}
	return runner.Register(HarnessAuthorizationMigrationID, func(db *gorm.DB, version string) error {
		return applyHarnessAuthorizationMigration(db, version)
	})
}

func applyHarnessAuthorizationMigration(db *gorm.DB, version string) error {
	return applyHarnessAuthorizationMigrationWithHook(db, version, nil)
}

// The hook is used only by rollback tests. Production always passes nil.
func applyHarnessAuthorizationMigrationWithHook(
	db *gorm.DB,
	version string,
	afterPolicies func() error,
) error {
	if db == nil {
		return errors.New("harness authorization migration database is required")
	}
	if version != HarnessAuthorizationMigrationID.String() {
		return errors.New("harness authorization migration version mismatch")
	}
	if err := verifyHarnessAuthorizationPrerequisites(db); err != nil {
		return err
	}

	return db.Transaction(func(tx *gorm.DB) error {
		var applied int64
		if err := tx.Model(new(migrationmodels.Migration)).Where("version = ?", version).Count(&applied).Error; err != nil {
			return fmt.Errorf("harness authorization migration: check version: %w", err)
		}
		if applied > 0 {
			return nil
		}

		role, err := resolveHarnessAuthorizationRole(tx, harnessDefaultRoleName)
		if err != nil {
			return err
		}
		menu, err := upsertHarnessAuthorizationMenu(tx, harnessAuthorizationMenuSeed{
			name:       "Harness Platform",
			path:       "/harness",
			method:     "GET",
			accessType: adminpkg.MenuAccessType,
			permission: PermissionRead,
			icon:       "RobotOutlined",
			sort:       45,
		})
		if err != nil {
			return err
		}
		if err := seedHarnessAuthorizationRule(tx, role.ID, adminpkg.MenuAccessType, menu.Path, menu.Method); err != nil {
			return err
		}

		components := make(map[string]*models.Menu, len(harnessPermissionSeeds))
		for _, seed := range harnessPermissionSeeds {
			component, err := upsertHarnessAuthorizationMenu(tx, harnessAuthorizationMenuSeed{
				name:       seed.displayName,
				path:       seed.componentPath,
				method:     "GET",
				parentID:   menu.ID,
				accessType: adminpkg.ComponentAccessType,
				permission: seed.code,
				hidden:     true,
			})
			if err != nil {
				return err
			}
			components[seed.code] = component
			if err := seedHarnessAuthorizationRule(tx, role.ID, adminpkg.ComponentAccessType, component.Path, component.Method); err != nil {
				return err
			}
		}

		for _, route := range harnessAuthorizationRoutes {
			component := components[route.permission]
			if component == nil {
				return fmt.Errorf("harness authorization migration: permission component %q is missing", route.permission)
			}
			if _, err := upsertHarnessAuthorizationMenu(tx, harnessAuthorizationMenuSeed{
				name:       route.method + " " + route.path,
				path:       route.path,
				method:     route.method,
				parentID:   component.ID,
				accessType: adminpkg.APIAccessType,
				permission: route.permission,
				hidden:     true,
			}); err != nil {
				return err
			}
			if err := seedHarnessAuthorizationRule(tx, role.ID, adminpkg.APIAccessType, route.path, route.method); err != nil {
				return err
			}
		}

		if afterPolicies != nil {
			if err := afterPolicies(); err != nil {
				return fmt.Errorf("harness authorization migration interrupted after policy seed: %w", err)
			}
		}
		if err := advanceHarnessAuthorizationRevision(tx, harnessAuthorizationScopeRole, role.ID); err != nil {
			return err
		}
		if err := advanceHarnessAuthorizationRevision(tx, harnessAuthorizationScopeGlobal, ""); err != nil {
			return err
		}

		versionRow := new(migrationmodels.Migration)
		versionRow.SetVersion(version)
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "version"}},
			DoNothing: true,
		}).Create(versionRow).Error; err != nil {
			return fmt.Errorf("harness authorization migration: record version: %w", err)
		}
		return nil
	})
}

func verifyHarnessAuthorizationPrerequisites(db *gorm.DB) error {
	if db == nil {
		return errors.New("harness authorization database is required")
	}
	for _, model := range []any{
		new(models.Role),
		new(models.Menu),
		new(models.CasbinRule),
		new(models.ConfigRevision),
		new(migrationmodels.Migration),
	} {
		if !db.Migrator().HasTable(model) {
			return fmt.Errorf("harness authorization migration prerequisite %T is unavailable", model)
		}
	}
	return nil
}

func verifyHarnessAuthorizationReadiness(db *gorm.DB) error {
	if err := verifyHarnessAuthorizationPrerequisites(db); err != nil {
		return fmt.Errorf("%w: %v", ErrHarnessAuthorizationUnavailable, err)
	}
	var role models.Role
	if err := db.Where("name = ? AND status = ?", harnessDefaultRoleName, enum.Enabled).Take(&role).Error; err != nil {
		return fmt.Errorf("%w: default admin role is unavailable", ErrHarnessAuthorizationUnavailable)
	}
	var menu models.Menu
	if err := db.Where(
		"type = ? AND path = ? AND method = ? AND permission = ? AND status = ?",
		adminpkg.MenuAccessType, "/harness", "GET", PermissionRead, enum.Enabled,
	).Take(&menu).Error; err != nil {
		return fmt.Errorf("%w: Harness menu authorization is unavailable", ErrHarnessAuthorizationUnavailable)
	}

	components := make(map[string]models.Menu, len(harnessPermissionSeeds))
	for _, seed := range harnessPermissionSeeds {
		var component models.Menu
		if err := db.Where(
			"type = ? AND path = ? AND method = ? AND permission = ? AND parent_id = ? AND status = ?",
			adminpkg.ComponentAccessType, seed.componentPath, "GET", seed.code, menu.ID, enum.Enabled,
		).Take(&component).Error; err != nil {
			return fmt.Errorf("%w: permission component %s is unavailable", ErrHarnessAuthorizationUnavailable, seed.code)
		}
		components[seed.code] = component
	}

	for _, route := range harnessAuthorizationRoutes {
		component := components[route.permission]
		var apiCount int64
		if err := db.Model(new(models.Menu)).Where(
			"type = ? AND path = ? AND method = ? AND permission = ? AND parent_id = ? AND status = ?",
			adminpkg.APIAccessType, route.path, route.method, route.permission, component.ID, enum.Enabled,
		).Count(&apiCount).Error; err != nil {
			return fmt.Errorf("%w: inspect API authorization %s %s", ErrHarnessAuthorizationUnavailable, route.method, route.path)
		}
		if apiCount != 1 {
			return fmt.Errorf("%w: API authorization %s %s is unavailable", ErrHarnessAuthorizationUnavailable, route.method, route.path)
		}
		var policyCount int64
		if err := db.Model(new(models.CasbinRule)).Where(
			"ptype = ? AND v0 = ? AND v1 = ? AND v2 = ? AND v3 = ?",
			"p", role.ID, adminpkg.APIAccessType.String(), route.path, route.method,
		).Count(&policyCount).Error; err != nil {
			return fmt.Errorf("%w: inspect API policy %s %s", ErrHarnessAuthorizationUnavailable, route.method, route.path)
		}
		if policyCount != 1 {
			return fmt.Errorf("%w: API policy %s %s is unavailable", ErrHarnessAuthorizationUnavailable, route.method, route.path)
		}
	}
	return nil
}

func resolveHarnessAuthorizationRole(tx *gorm.DB, name string) (*models.Role, error) {
	var matches []models.Role
	if err := tx.Unscoped().Where("name = ?", name).Order("id").Limit(2).Find(&matches).Error; err != nil {
		return nil, fmt.Errorf("harness authorization migration: resolve role %q: %w", name, err)
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("harness authorization migration: role %q is ambiguous", name)
	}
	if len(matches) == 1 {
		role := &matches[0]
		if role.DeletedAt.Valid || role.Status != enum.Enabled {
			return nil, fmt.Errorf("harness authorization migration: role %q is not active", name)
		}
		return role, nil
	}
	role := &models.Role{
		Name:   name,
		Status: enum.Enabled,
		Remark: "Harness Platform default administrator role",
	}
	if err := tx.Create(role).Error; err != nil {
		return nil, fmt.Errorf("harness authorization migration: create role %q: %w", name, err)
	}
	return role, nil
}

type harnessAuthorizationMenuSeed struct {
	name       string
	path       string
	method     string
	parentID   string
	accessType adminpkg.AccessType
	permission string
	icon       string
	sort       int
	hidden     bool
}

func upsertHarnessAuthorizationMenu(tx *gorm.DB, seed harnessAuthorizationMenuSeed) (*models.Menu, error) {
	query := tx.Unscoped().Where("type = ? AND path = ?", seed.accessType, seed.path)
	if seed.accessType == adminpkg.APIAccessType {
		query = query.Where("method = ?", seed.method)
	}
	var matches []models.Menu
	if err := query.Order("id").Limit(2).Find(&matches).Error; err != nil {
		return nil, fmt.Errorf("harness authorization migration: resolve %s %q: %w", seed.accessType, seed.path, err)
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("harness authorization migration: %s %q is ambiguous", seed.accessType, seed.path)
	}
	if len(matches) == 0 {
		menu := &models.Menu{
			Name:       seed.name,
			Path:       seed.path,
			Method:     seed.method,
			ParentID:   seed.parentID,
			Icon:       seed.icon,
			Type:       seed.accessType,
			Permission: seed.permission,
			Status:     enum.Enabled,
			Sort:       seed.sort,
			HideInMenu: seed.hidden,
		}
		if err := tx.Create(menu).Error; err != nil {
			return nil, fmt.Errorf("harness authorization migration: create %s %q: %w", seed.accessType, seed.path, err)
		}
		return menu, nil
	}
	menu := &matches[0]
	if menu.DeletedAt.Valid {
		return nil, fmt.Errorf("harness authorization migration: %s %q is soft-deleted", seed.accessType, seed.path)
	}
	if err := tx.Model(menu).Updates(map[string]any{
		"name":         seed.name,
		"method":       seed.method,
		"parent_id":    seed.parentID,
		"icon":         seed.icon,
		"permission":   seed.permission,
		"status":       enum.Enabled,
		"sort":         seed.sort,
		"hide_in_menu": seed.hidden,
	}).Error; err != nil {
		return nil, fmt.Errorf("harness authorization migration: update %s %q: %w", seed.accessType, seed.path, err)
	}
	return menu, nil
}

func seedHarnessAuthorizationRule(tx *gorm.DB, roleID string, accessType adminpkg.AccessType, path, method string) error {
	if strings.TrimSpace(roleID) == "" {
		return errors.New("harness authorization migration: role ID is empty")
	}
	rule := &models.CasbinRule{
		PType: "p",
		V0:    roleID,
		V1:    accessType.String(),
		V2:    path,
		V3:    method,
	}
	if err := tx.Where(
		"ptype = ? AND v0 = ? AND v1 = ? AND v2 = ? AND v3 = ?",
		rule.PType, rule.V0, rule.V1, rule.V2, rule.V3,
	).FirstOrCreate(rule).Error; err != nil {
		return fmt.Errorf("harness authorization migration: seed %s %s %s: %w", accessType, method, path, err)
	}
	return nil
}

func advanceHarnessAuthorizationRevision(tx *gorm.DB, scope, ownerID string) error {
	now := time.Now().UTC()
	key := &models.ConfigRevision{
		Scope:     scope,
		OwnerID:   ownerID,
		Resource:  harnessAuthorizationResource,
		Revision:  0,
		UpdatedAt: now,
	}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(key).Error; err != nil {
		return fmt.Errorf("harness authorization migration: ensure revision %s/%s: %w", scope, ownerID, err)
	}
	var current models.ConfigRevision
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
		"scope = ? AND owner_id = ? AND resource = ?",
		scope, ownerID, harnessAuthorizationResource,
	).Take(&current).Error; err != nil {
		return fmt.Errorf("harness authorization migration: lock revision %s/%s: %w", scope, ownerID, err)
	}
	if current.Revision < 0 || current.Revision == 1<<63-1 {
		return fmt.Errorf("harness authorization migration: revision cannot advance for %s/%s", scope, ownerID)
	}
	result := tx.Model(new(models.ConfigRevision)).Where(
		"scope = ? AND owner_id = ? AND resource = ? AND revision = ?",
		scope, ownerID, harnessAuthorizationResource, current.Revision,
	).Updates(map[string]any{
		"revision":   current.Revision + 1,
		"updated_at": now,
	})
	if result.Error != nil {
		return fmt.Errorf("harness authorization migration: advance revision %s/%s: %w", scope, ownerID, result.Error)
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("harness authorization migration: revision changed concurrently for %s/%s", scope, ownerID)
	}
	return nil
}
