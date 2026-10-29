package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	"github.com/onsi/ginkgo/v2/reporters"
	. "github.com/onsi/gomega"
	"github.com/openshift-dpf/test/reporting"
)

var executionReport Report
var executionReportDir string

var _ = ReportBeforeSuite(func(report Report) {
	base := os.Getenv("ARTIFACT_DIR")
	if base == "" {
		base = filepath.Join("..", "..", "artifacts")
	}
	var err error
	executionReportDir, err = filepath.Abs(filepath.Join(base, "e2e"))
	Expect(err).NotTo(HaveOccurred())
	executionReport = report
	Expect(reporting.WriteHTML(filepath.Join(executionReportDir, "report.html"), report, false)).To(Succeed())
	fmt.Fprintf(GinkgoWriter, "E2E HTML log: %s\n", filepath.Join(executionReportDir, "report.html"))
})

var _ = ReportAfterEach(func(spec SpecReport) {
	// Parallel workers are aggregated by Ginkgo for the final report.
	if GinkgoParallelProcess() != 1 {
		return
	}
	executionReport.SpecReports = append(executionReport.SpecReports, spec)
	executionReport.EndTime = time.Now()
	executionReport.RunTime = executionReport.EndTime.Sub(executionReport.StartTime)
	if err := reporting.WriteHTML(filepath.Join(executionReportDir, "report.html"), executionReport, false); err != nil {
		fmt.Fprintf(GinkgoWriter, "WARNING: updating E2E HTML log: %v\n", err)
	}
})

var _ = ReportAfterSuite("Write E2E execution reports", func(report Report) {
	Expect(reporters.GenerateJSONReport(report, filepath.Join(executionReportDir, "report.json"))).To(Succeed())
	Expect(reporters.GenerateJUnitReport(report, filepath.Join(executionReportDir, "junit_e2e.xml"))).To(Succeed())
	Expect(reporting.WriteHTML(filepath.Join(executionReportDir, "report.html"), report, true)).To(Succeed())
	fmt.Printf("E2E reports: %s\n", executionReportDir)
})
