package reporting

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onsi/ginkgo/v2/types"
)

func TestHTMLReport(t *testing.T) {
	report := types.Report{
		SuiteDescription: "DPF report test",
		SpecReports: types.SpecReports{
			{ContainerHierarchyTexts: []string{"Sanity"}, LeafNodeType: types.NodeTypeIt, LeafNodeText: "passes", State: types.SpecStatePassed},
			{ContainerHierarchyTexts: []string{"TC-SVC-004"}, LeafNodeType: types.NodeTypeIt, LeafNodeText: "fails", State: types.SpecStateFailed,
				Failure:                    types.Failure{Message: "<script>alert('unsafe')</script>", Location: types.CodeLocation{FileName: "test.go", LineNumber: 42}},
				SpecEvents:                 types.SpecEvents{{SpecEventType: types.SpecEventByStart, Message: "Discover HBN pods"}},
				CapturedGinkgoWriterOutput: "\x1b[31mHBN lookup failed\x1b[0m"},
			{LeafNodeType: types.NodeTypeAfterSuite, State: types.SpecStateFailed, Failure: types.Failure{Message: "cleanup failed"}},
			{ContainerHierarchyTexts: []string{"Skipped case"}, LeafNodeType: types.NodeTypeIt, State: types.SpecStateSkipped},
		},
	}
	for i := range report.SpecReports {
		spec := &report.SpecReports[i]
		levels := len(spec.ContainerHierarchyTexts)
		spec.ContainerHierarchyLocations = make([]types.CodeLocation, levels)
		spec.ContainerHierarchyLabels = make([][]string, levels)
		spec.ContainerHierarchySemVerConstraints = make([][]string, levels)
		spec.ContainerHierarchyComponentSemVerConstraints = make([]map[string][]string, levels)
		if spec.Failed() {
			spec.Failure.FailureNodeContext = types.FailureNodeIsLeafNode
			spec.Failure.FailureNodeType = spec.LeafNodeType
		}
	}
	destination := filepath.Join(t.TempDir(), "e2e", "report.html")
	for _, complete := range []bool{false, true} {
		if err := WriteHTML(destination, report, complete); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(destination)
		if err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(destination)
		if err != nil || info.Mode().Perm() != 0o644 {
			t.Fatal("HTML report must be readable by the artifact uploader")
		}
		html := string(data)
		for _, want := range []string{"&lt;script&gt;", "test.go:42", "HBN lookup failed", "Discover HBN pods", "cleanup failed", "<b>1</b> passed", "<b>1</b> failed specs", "<b>1</b> skipped / filtered"} {
			if !strings.Contains(html, want) {
				t.Errorf("missing %q", want)
			}
		}
		if strings.Contains(html, "<script>") || strings.Contains(html, "\x1b") {
			t.Fatal("report contains unescaped HTML or ANSI escapes")
		}
		if strings.Contains(html, "INCOMPLETE SNAPSHOT") == complete {
			t.Fatal("completion state is misleading")
		}
		if strings.Index(html, "TC-SVC-004") > strings.Index(html, "<summary>Sanity") {
			t.Fatal("failure groups must precede passed groups")
		}
	}
	if err := WriteHTML(filepath.Join(destination, "impossible.html"), report, true); err == nil {
		t.Fatal("report write errors must propagate")
	}
	data, err := os.ReadFile(destination)
	if err != nil || !strings.Contains(string(data), "Suite failed") {
		t.Fatal("a failed write damaged the previous snapshot")
	}
}
