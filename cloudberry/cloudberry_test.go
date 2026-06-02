package cloudberry

import (
	"strings"
	"testing"
)

func TestParseVersion(t *testing.T) {
	tests := []struct {
		name       string
		raw        string
		wantFlavor Flavor
		wantMajor  int
		wantMinor  int
		wantPatch  int
		isMPP      bool
	}{
		{
			name:       "cloudberry 2.1.0",
			raw:        "PostgreSQL 14.4 (Apache Cloudberry 2.1.0-incubating build bdf90c5518f) on aarch64-unknown-linux-gnu",
			wantFlavor: FlavorCloudberry,
			wantMajor:  2, wantMinor: 1, wantPatch: 0,
			isMPP: true,
		},
		{
			name:       "greenplum 7.1.0",
			raw:        "PostgreSQL 12.12 (Greenplum Database 7.1.0 build commit) on x86_64",
			wantFlavor: FlavorGreenplum,
			wantMajor:  7, wantMinor: 1, wantPatch: 0,
			isMPP: true,
		},
		{
			name:       "vanilla postgres",
			raw:        "PostgreSQL 16.2 on x86_64-pc-linux-gnu",
			wantFlavor: FlavorPostgres,
			isMPP:      false,
		},
		{
			name:       "unknown",
			raw:        "Some Other Database 1.0",
			wantFlavor: FlavorUnknown,
			isMPP:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := parseVersion(tt.raw)
			if v.Flavor != tt.wantFlavor {
				t.Errorf("Flavor = %q, want %q", v.Flavor, tt.wantFlavor)
			}
			if tt.wantFlavor == FlavorCloudberry || tt.wantFlavor == FlavorGreenplum {
				if v.Major != tt.wantMajor || v.Minor != tt.wantMinor || v.Patch != tt.wantPatch {
					t.Errorf("version = %d.%d.%d, want %d.%d.%d",
						v.Major, v.Minor, v.Patch, tt.wantMajor, tt.wantMinor, tt.wantPatch)
				}
			}
			if v.IsMPP() != tt.isMPP {
				t.Errorf("IsMPP() = %v, want %v", v.IsMPP(), tt.isMPP)
			}
			if v.IsCloudberry() != (tt.wantFlavor == FlavorCloudberry) {
				t.Errorf("IsCloudberry() = %v", v.IsCloudberry())
			}
			if v.IsGreenplum() != (tt.wantFlavor == FlavorGreenplum) {
				t.Errorf("IsGreenplum() = %v", v.IsGreenplum())
			}
			if v.Raw != tt.raw {
				t.Errorf("Raw not preserved")
			}
		})
	}
}

func TestSegmentHelpers(t *testing.T) {
	coord := Segment{Content: -1, Role: RolePrimary, Status: "u"}
	if !coord.IsCoordinator() {
		t.Error("content -1 should be coordinator")
	}
	if !coord.IsUp() {
		t.Error("status u should be up")
	}
	seg := Segment{Content: 0, Role: RolePrimary, Status: "d"}
	if seg.IsCoordinator() {
		t.Error("content 0 is not coordinator")
	}
	if seg.IsUp() {
		t.Error("status d should be down")
	}
}

func TestSnapshotIDValidation(t *testing.T) {
	valid := []string{
		"0000000A-000043EF-1",
		"00000001-00000002-3",
		"FFFFFFFF-FFFFFFFF-A",
	}
	for _, s := range valid {
		if !snapshotIDRe.MatchString(s) {
			t.Errorf("snapshot id %q should be valid", s)
		}
	}
	invalid := []string{
		"",
		"not-a-snapshot",
		"0000000A-000043EF",         // too few parts
		"'; DROP TABLE x; --",       // injection attempt
		"0000000A-000043EF-1; evil", // trailing junk
	}
	for _, s := range invalid {
		if snapshotIDRe.MatchString(s) {
			t.Errorf("snapshot id %q should be invalid", s)
		}
	}
}

func TestQuoteLiteral(t *testing.T) {
	tests := map[string]string{
		"plain":               "'plain'",
		"with'quote":          "'with''quote'",
		"cat > /tmp/f_<SEGID>": "'cat > /tmp/f_<SEGID>'",
	}
	for in, want := range tests {
		if got := quoteLiteral(in); got != want {
			t.Errorf("quoteLiteral(%q) = %q, want %q", in, got, want)
		}
	}
}

// fakeExecer records the SQL passed to ExecContext and returns a canned result.
type fakeExecer struct {
	lastQuery string
}

func (f *fakeExecer) ExecContext(_ ctxType, query string, _ ...any) (resultType, error) {
	f.lastQuery = query
	return fakeResult{}, nil
}

func TestCopyToProgramOnSegment_BuildsStatement(t *testing.T) {
	f := &fakeExecer{}
	err := CopyToProgramOnSegment(ctxBackground(), f, "public.customers", "cat > /b/seg_<SEGID>.dat")
	if err != nil {
		t.Fatalf("CopyToProgramOnSegment: %v", err)
	}
	want := "COPY public.customers TO PROGRAM 'cat > /b/seg_<SEGID>.dat' ON SEGMENT"
	if f.lastQuery != want {
		t.Errorf("statement = %q, want %q", f.lastQuery, want)
	}
}

func TestCopyFromProgramOnSegment_BuildsStatement(t *testing.T) {
	f := &fakeExecer{}
	err := CopyFromProgramOnSegment(ctxBackground(), f, "public.orders", "cat /b/seg_<SEGID>.dat")
	if err != nil {
		t.Fatalf("CopyFromProgramOnSegment: %v", err)
	}
	want := "COPY public.orders FROM PROGRAM 'cat /b/seg_<SEGID>.dat' ON SEGMENT"
	if f.lastQuery != want {
		t.Errorf("statement = %q, want %q", f.lastQuery, want)
	}
}

func TestCopyOnSegment_RequiresSegIDToken(t *testing.T) {
	f := &fakeExecer{}
	if err := CopyToProgramOnSegment(ctxBackground(), f, "t", "cat > /b/file.dat"); err == nil {
		t.Error("expected error when program lacks <SEGID> token")
	}
	if err := CopyFromProgramOnSegment(ctxBackground(), f, "t", "cat /b/file.dat"); err == nil {
		t.Error("expected error when program lacks <SEGID> token")
	}
}

func TestSetSerializable(t *testing.T) {
	f := &fakeExecer{}
	if err := SetSerializable(ctxBackground(), f); err != nil {
		t.Fatalf("SetSerializable: %v", err)
	}
	if !strings.Contains(f.lastQuery, "serializable") {
		t.Errorf("statement = %q, want serializable", f.lastQuery)
	}
}
