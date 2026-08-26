package httpapi

import (
	"io/fs"
	"strings"
	"testing"
)

func TestUploadUIReportsProgressBeforeJobCreation(t *testing.T) {
	assets, err := fs.Sub(webAssets, "web")
	if err != nil {
		t.Fatal(err)
	}
	script, err := fs.ReadFile(assets, "app.js")
	if err != nil {
		t.Fatal(err)
	}

	source := string(script)
	for _, required := range []string{
		"new XMLHttpRequest()",
		"renderUpload(0, file.size)",
		"正在上传 PDF",
		"已上传 ${formatBytes(loaded)} / ${formatBytes(total)}（${percent}%）",
		"取消上传",
	} {
		if !strings.Contains(source, required) {
			t.Errorf("app.js does not contain upload feedback %q", required)
		}
	}

	if strings.Index(source, "renderUpload(0, file.size)") > strings.Index(source, "await uploadJob(form, uploadTicket, renderUpload)") {
		t.Error("initial upload feedback must render before awaiting the upload request")
	}

	markup, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, versionedAsset := range []string{"/app.js?v=voucher-altcha-1", "/mode.css?v=voucher-altcha-1"} {
		if !strings.Contains(string(markup), versionedAsset) {
			t.Errorf("index.html does not cache-bust changed asset %q", versionedAsset)
		}
	}
}
