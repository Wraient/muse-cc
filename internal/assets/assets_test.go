package assets

import (
	"io/fs"
	"testing"
	"testing/fstest"
)

func TestPanelFileSystemRequiresIndex(t *testing.T) {
	tests := []struct {
		name string
		root fs.FS
		ok   bool
	}{
		{
			name: "empty",
			root: fstest.MapFS{},
			ok:   false,
		},
		{
			name: "assets without index",
			root: fstest.MapFS{
				"assets/app.js": &fstest.MapFile{Data: []byte("console.log('ok')")},
			},
			ok: false,
		},
		{
			name: "complete panel",
			root: fstest.MapFS{
				"index.html": &fstest.MapFile{Data: []byte("<div id=\"root\"></div>")},
			},
			ok: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ok := panelFileSystem(tt.root)
			if ok != tt.ok {
				t.Fatalf("panelFileSystem() ok = %v, want %v", ok, tt.ok)
			}
		})
	}
}
