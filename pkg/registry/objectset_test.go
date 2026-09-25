package registry

import (
	"reflect"
	"strings"
	"testing"

	"github.com/harness/cli/v3/pkg/spec"
)

// fmeTags and fmeOwners mirror the object_set field definitions in fme.spec.yaml, so
// these tests exercise the generic machinery against the member syntax FME actually
// ships. Drift between the two is caught by the TestFMESpec_* tests in pkg/specloader,
// which drive the real spec file end to end.
var (
	fmeTags = spec.FieldDef{
		ID:             "tags",
		FieldType:      "object_set",
		MemberExpr:     `{name: member}`,
		MemberIdentity: `el.name`,
		MemberHint:     `<tag-name>`,
	}
	fmeOwners = spec.FieldDef{
		ID:        "owners",
		FieldType: "object_set",
		MemberExpr: `lower(split(member, ":")[0]) == "user" ? {type: "USER", id: member[5:]} : ` +
			`lower(split(member, ":")[0]) == "group" ? {type: "GROUP", identifier: member[6:]} : nil`,
		MemberIdentity: `el.type == "USER" ? "user:" + el.id : "group:" + el.identifier`,
		MemberHint:     `user:<id> or group:<identifier>`,
	}
)

func TestObjectSetMember(t *testing.T) {
	tests := []struct {
		name         string
		fd           spec.FieldDef
		member       string
		want         any
		wantIdentity string
		wantErr      string
	}{
		{
			name:         "tag member is just its name",
			fd:           fmeTags,
			member:       "backend",
			want:         map[string]any{"name": "backend"},
			wantIdentity: "backend",
		},
		{
			name:         "user by id",
			fd:           fmeOwners,
			member:       "user:rkLvGn_STT6gEhsdo6kRdg",
			want:         map[string]any{"type": "USER", "id": "rkLvGn_STT6gEhsdo6kRdg"},
			wantIdentity: "user:rkLvGn_STT6gEhsdo6kRdg",
		},
		{
			// An email is accepted syntactically but sent as an id, which the API will
			// reject. Owners are addressed by id only; use "harness list user" to
			// resolve a person to their id.
			name:         "email is passed through as id",
			fd:           fmeOwners,
			member:       "user:alice@example.com",
			want:         map[string]any{"type": "USER", "id": "alice@example.com"},
			wantIdentity: "user:alice@example.com",
		},
		{
			name:         "group uses identifier, not id",
			fd:           fmeOwners,
			member:       "group:_project_all_users",
			want:         map[string]any{"type": "GROUP", "identifier": "_project_all_users"},
			wantIdentity: "group:_project_all_users",
		},
		{
			name:         "prefix is case-insensitive",
			fd:           fmeOwners,
			member:       "USER:u1",
			want:         map[string]any{"type": "USER", "id": "u1"},
			wantIdentity: "user:u1",
		},
		{
			name:    "missing prefix is rejected with the spec's hint",
			fd:      fmeOwners,
			member:  "u1",
			wantErr: `"u1" is not a valid owners member (expected user:<id> or group:<identifier>)`,
		},
		{
			name:    "unknown prefix is rejected",
			fd:      fmeOwners,
			member:  "team:platform",
			wantErr: `"team:platform" is not a valid owners member`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, identity, err := objectSetMember(map[string]any{}, tt.fd, tt.member)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("objectSetMember(%q) error = %v, want error containing %q", tt.member, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("objectSetMember(%q): %v", tt.member, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("objectSetMember(%q) object = %v, want %v", tt.member, got, tt.want)
			}
			if identity != tt.wantIdentity {
				t.Errorf("objectSetMember(%q) identity = %q, want %q", tt.member, identity, tt.wantIdentity)
			}
		})
	}
}

// TestObjectSetIdentityAcrossShapes is the reason member_identity exists: the array
// being mutated holds what the API returned, which carries fields the write shape does
// not, so members can only be compared through a projection both shapes agree on.
func TestObjectSetIdentityAcrossShapes(t *testing.T) {
	tests := []struct {
		name string
		fd   spec.FieldDef
		el   any
		want string
	}{
		{
			name: "tag read shape carries an id the write shape drops",
			fd:   fmeTags,
			el:   map[string]any{"id": "t1", "name": "backend"},
			want: "backend",
		},
		{
			name: "user read shape carries a name the write shape drops",
			fd:   fmeOwners,
			el:   map[string]any{"type": "USER", "id": "u1", "name": "alice"},
			want: "user:u1",
		},
		{
			name: "group",
			fd:   fmeOwners,
			el:   map[string]any{"type": "GROUP", "identifier": "g1"},
			want: "group:g1",
		},
		{
			// Nothing the expression can read: never equal to a real member, so it is
			// left alone by --del and never suppresses a --set.
			name: "unreadable element has no identity",
			fd:   fmeOwners,
			el:   "not-an-object",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := objectSetIdentity(map[string]any{}, tt.fd, tt.el); got != tt.want {
				t.Errorf("objectSetIdentity(%v) = %q, want %q", tt.el, got, tt.want)
			}
		})
	}
}

func TestObjectSetContainsAndRemove(t *testing.T) {
	// A realistic read-shaped array: what update_body_pick hands to applyMutations.
	owners := []any{
		map[string]any{"type": "USER", "id": "u1", "name": "alice"},
		map[string]any{"type": "GROUP", "identifier": "g1"},
	}

	for _, tt := range []struct {
		identity string
		want     bool
	}{
		{"user:u1", true},
		{"group:g1", true},
		{"user:u2", false},
		{"group:g2", false},
		// A group identifier must not match a user carrying the same string as its id.
		{"group:u1", false},
	} {
		if got := objectSetContains(map[string]any{}, fmeOwners, owners, tt.identity); got != tt.want {
			t.Errorf("objectSetContains(%q) = %v, want %v", tt.identity, got, tt.want)
		}
	}

	got := objectSetRemove(map[string]any{}, fmeOwners, owners, "user:u1")
	want := []any{map[string]any{"type": "GROUP", "identifier": "g1"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("objectSetRemove(user:u1) = %v, want %v", got, want)
	}

	// Removing something absent leaves the array intact rather than erroring.
	if got := objectSetRemove(map[string]any{}, fmeOwners, owners, "user:nobody"); len(got) != 2 {
		t.Errorf("objectSetRemove(user:nobody) = %v, want the array unchanged", got)
	}
}

// TestFieldDefValidateObjectSet asserts a spec cannot declare an object_set field
// without the two expressions it needs, which would otherwise only fail at the moment
// someone tries to mutate it.
func TestFieldDefValidateObjectSet(t *testing.T) {
	for _, tt := range []struct {
		name    string
		fd      spec.FieldDef
		wantErr bool
	}{
		{name: "complete", fd: fmeTags},
		{
			name:    "missing member_identity",
			fd:      spec.FieldDef{ID: "tags", FieldType: "object_set", MemberExpr: "{name: member}"},
			wantErr: true,
		},
		{
			name:    "missing member_expr",
			fd:      spec.FieldDef{ID: "tags", FieldType: "object_set", MemberIdentity: "el.name"},
			wantErr: true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fd := tt.fd
			fd.Expr = "it.tags" // Validate requires expr on every field.
			err := fd.Validate()
			if tt.wantErr && err == nil {
				t.Fatal("Validate() = nil, want an error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
		})
	}
}

// TestApplyMutationsSeedsCollection covers mutable_seed_expr: a collection left out of
// update_body_pick is seeded from the GET response, but only when a mutation names it and
// only when the picked body says nothing about it. That is what lets a pick stay minimal —
// an update that touches nothing else sends no collection at all.
func TestApplyMutationsSeedsCollection(t *testing.T) {
	// The write shape is narrower than the read shape, which is the whole reason the seed is
	// an expression and not a copy of the GET subtree.
	seededOwners := fmeOwners
	seededOwners.MutablePath = "owners"
	seededOwners.MutableSeedExpr = `it.owners == nil ? [] : map(it.owners, ({type: "USER", id: #.id}))`
	seededTags := fmeTags
	seededTags.MutablePath = "tags"
	seededTags.MutableSeedExpr = `it.tags == nil ? [] : map(it.tags, ({name: #.name}))`

	getResult := map[string]any{
		"description": "current",
		"owners":      []any{map[string]any{"id": "u1", "type": "USER", "name": "Ann", "email": "ann@x.io"}},
		"tags":        []any{map[string]any{"id": "t1", "name": "alpha"}},
	}
	fields := map[string]spec.FieldDef{
		"owners": seededOwners,
		"tags":   seededTags,
		// A scalar alongside them: --set creates its path, so it never seeds.
		"description": {ID: "description", MutablePath: "description"},
	}

	tests := []struct {
		name     string
		mutable  map[string]any
		setArgs  map[string]string
		delArgs  []string
		seedRoot any
		want     map[string]any
	}{
		{
			name:     "no mutation of the field leaves it out entirely",
			mutable:  map[string]any{"description": "new"},
			setArgs:  map[string]string{"description": "new"},
			seedRoot: getResult,
			want:     map[string]any{"description": "new"},
		},
		{
			name:     "--set seeds the current members then appends",
			mutable:  map[string]any{},
			setArgs:  map[string]string{"owners.user:u2": ""},
			seedRoot: getResult,
			want: map[string]any{"owners": []any{
				map[string]any{"type": "USER", "id": "u1"},
				map[string]any{"type": "USER", "id": "u2"},
			}},
		},
		{
			name:     "--del seeds so the removal has something to remove from",
			mutable:  map[string]any{},
			delArgs:  []string{"owners.user:u1"},
			seedRoot: getResult,
			want:     map[string]any{"owners": []any{}},
		},
		{
			// A pick that already selected the field wins: seeding an explicit empty
			// collection would resurrect members the pick deliberately dropped.
			name:     "a value already in the picked body is not overwritten",
			mutable:  map[string]any{"owners": []any{}},
			setArgs:  map[string]string{"owners.user:u2": ""},
			seedRoot: getResult,
			want:     map[string]any{"owners": []any{map[string]any{"type": "USER", "id": "u2"}}},
		},
		{
			// create has no GET to seed from, so the collection correctly starts empty.
			name:     "nil seedRoot starts from empty",
			mutable:  map[string]any{},
			setArgs:  map[string]string{"owners.user:u2": ""},
			seedRoot: nil,
			want:     map[string]any{"owners": []any{map[string]any{"type": "USER", "id": "u2"}}},
		},
		{
			name:     "seeding is per field, not all collections at once",
			mutable:  map[string]any{},
			setArgs:  map[string]string{"tags.beta": ""},
			seedRoot: getResult,
			want: map[string]any{"tags": []any{
				map[string]any{"name": "alpha"},
				map[string]any{"name": "beta"},
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := applyMutations(nil, tt.mutable, tt.setArgs, tt.delArgs, fields, tt.seedRoot); err != nil {
				t.Fatalf("applyMutations: %v", err)
			}
			if !reflect.DeepEqual(tt.mutable, tt.want) {
				t.Errorf("body = %#v, want %#v", tt.mutable, tt.want)
			}
		})
	}
}
