package models

import (
	"testing"

	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
)

func TestResolvePreset(t *testing.T) {
	tests := []struct {
		name   string
		preset string
		want   []Permission
	}{
		{name: "viewer", preset: "viewer", want: PresetViewer},
		{name: "editor", preset: "editor", want: PresetEditor},
		{name: "approver", preset: "approver", want: PresetApprover},
		{name: "admin", preset: "admin", want: PresetAdmin},
		{name: "unknown", preset: "unknown", want: nil},
		{name: "empty", preset: "", want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ResolvePreset(tt.preset)
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestIsValidPermission(t *testing.T) {
	tests := []struct {
		name string
		perm string
		want bool
	}{
		{name: "valid route.view", perm: "route.view", want: true},
		{name: "valid route.create", perm: "route.create", want: true},
		{name: "valid client.approve", perm: "client.approve", want: true},
		{name: "valid audit.view", perm: "audit.view", want: true},
		{name: "valid project.settings", perm: "project.settings", want: true},
		{name: "invalid permission", perm: "route.invalid", want: false},
		{name: "empty string", perm: "", want: false},
		{name: "random string", perm: "foobar", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsValidPermission(tt.perm))
		})
	}
}

func TestProjectTeamRole_GetEffectivePermissions(t *testing.T) {
	tests := []struct {
		name    string
		presets []ProjectTeamPreset
		want    int // expected count (sorted, so just check length)
	}{
		{
			name:    "no presets",
			presets: nil,
			want:    0,
		},
		{
			name: "single preset with permissions",
			presets: []ProjectTeamPreset{
				{
					Preset: PermissionPreset{
						Permissions: pq.StringArray{"route.view", "route.create"},
					},
				},
			},
			want: 2,
		},
		{
			name: "multiple presets with overlap",
			presets: []ProjectTeamPreset{
				{
					Preset: PermissionPreset{
						Permissions: pq.StringArray{"route.view", "route.create"},
					},
				},
				{
					Preset: PermissionPreset{
						Permissions: pq.StringArray{"route.view", "client.view"},
					},
				},
			},
			want: 3, // route.view deduplicated
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ptr := &ProjectTeamRole{Presets: tt.presets}
			perms := ptr.GetEffectivePermissions()
			assert.Len(t, perms, tt.want)

			// Verify sorted
			for i := 1; i < len(perms); i++ {
				assert.True(t, string(perms[i-1]) < string(perms[i]), "permissions should be sorted")
			}
		})
	}
}

func TestProjectTeamRole_HasPermission(t *testing.T) {
	ptr := &ProjectTeamRole{
		Presets: []ProjectTeamPreset{
			{
				Preset: PermissionPreset{
					Permissions: pq.StringArray{"route.view", "route.create", "client.view"},
				},
			},
		},
	}

	assert.True(t, ptr.HasPermission(PermRouteView))
	assert.True(t, ptr.HasPermission(PermRouteCreate))
	assert.True(t, ptr.HasPermission(PermClientView))
	assert.False(t, ptr.HasPermission(PermRouteDeploy))
	assert.False(t, ptr.HasPermission(PermAuditView))
}

func TestProjectTeamRole_HasPermission_Empty(t *testing.T) {
	ptr := &ProjectTeamRole{}
	assert.False(t, ptr.HasPermission(PermRouteView))
}
