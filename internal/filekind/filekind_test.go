package filekind

import "testing"

func TestGenerated(t *testing.T) {
	for p, want := range map[string]bool{
		"package-lock.json":                           true,
		"web/Yarn.lock":                               true,
		"go.sum":                                      true,
		"static/app.min.js":                           true,
		"dist/app.js.map":                             true,
		"src/__snapshots__/card.test.ts.snap":         true,
		"Forms/Main.Designer.cs":                      true,
		"obj/Api.g.cs":                                true,
		"api/v1/pay.pb.go":                            true,
		"proto/pay_pb2.py":                            true,
		"Migrations/PayContextModelSnapshot.cs":       true,
		"vendor/github.com/x/y/z.go":                  true,
		"web/node_modules/left-pad/index.js":          true,
		"libs/third_party/zlib/inflate.c":             true,
		"packages/ui/dist/index.js":                   true,
		"src/app/app.component.ts":                    false,
		"Migrations/20240101_AddPayments.cs":          false,
		"docs/distribution.md":                        false,
		"src/vendors/list.ts":                         false,
		"cmd/footprint/main.go":                       false,
		"src/Distributed/Cache.cs":                    false,
		"vendor.go":                                   false,
		"README.md":                                   false,
		"src/app/generated-looking-but-written.ts":    false,
		"infra/modules/dist-lists/main.tf":            false,
		"services/node_modules_cleanup/cleanup.sh":    false,
		"test/fixtures/third_party_payload.json":      false,
		"pkg/api/handler_test.go":                     false,
		"src/Mapper.cs":                               false,
		"src/app/map.ts":                              false,
		"styles/site.css":                             false,
		"tools/snapshot.ts":                           false,
		"web/src/designer/Designer.tsx":               false,
		"proto/pay.proto":                             false,
		"Migrations/20240101_AddPayments.Designer.cs": true,
	} {
		if got := Generated(p); got != want {
			t.Errorf("Generated(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestBulky(t *testing.T) {
	if Bulky(BulkLines, 0) || !Bulky(BulkLines, 1) {
		t.Error("Bulky should be true only above BulkLines")
	}
}
