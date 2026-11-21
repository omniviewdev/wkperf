package cmd

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTargetsKeepNamesWithinRows(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		const row = `<tbody><tr><td class="data"><div class="targetname">%s</div><div class="targeturl"></div></td>
<td class="input"><input type="button" value="Inspect" onclick="window.open('Main.html?ws=' + window.location.host + '/socket/1/%d/%s', '_blank');"></td></tr></tbody>`
		fmt.Fprint(w, "<h1>Inspectable targets</h1><table>")
		fmt.Fprintf(w, row, "worker", 3, "ServiceWorker")
		fmt.Fprintf(w, row, "page &amp; title", 4, "WebPage")
		fmt.Fprint(w, `</table>`)
	}))
	defer srv.Close()

	command := newTargetsCmd()
	var out bytes.Buffer
	command.SetOut(&out)
	command.SetArgs([]string{srv.URL})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("[{\"name\":\"page \\u0026 title\",\"endpoint\":\"ws%s/socket/1/4/WebPage\"}]\n", strings.TrimPrefix(srv.URL, "http"))
	if out.String() != want {
		t.Fatalf("targets: got %s want %s", &out, want)
	}
}
