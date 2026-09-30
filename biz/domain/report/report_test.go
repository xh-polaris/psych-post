package report

import "testing"

func TestExtraReportNormalizesObjectShapedAnalysisEmotion(t *testing.T) {
	input := `{
        "title":"测试报告",
        "analysis":{"emotion":{"type":["平静","轻松"],"intensity":[1,0.5]}},
        "simple_report":{
          "emotion":["平静","轻松"],
          "riskLevel":0,
          "distressLevel":0,
          "suggestions":[]
        }
      }`

	report, err := extraReport(input)
	if err != nil {
		t.Fatalf("extraReport() error = %v", err)
	}
	if len(report.Analysis.Emotion) != 2 || report.Analysis.Emotion[0].Type != "平静" || report.Analysis.Emotion[1].Type != "轻松" {
		t.Fatalf("analysis.emotion = %#v, want normalized object array", report.Analysis.Emotion)
	}
	if report.Analysis.Emotion[0].Intensity != 1 || report.Analysis.Emotion[1].Intensity != 0.5 {
		t.Fatalf("analysis.emotion intensity = %#v, want [1 0.5]", report.Analysis.Emotion)
	}
}

func TestExtraReportNormalizesLegacySimpleReportEmotion(t *testing.T) {
	input := `{
        "title":"测试报告",
        "analysis":{"emotion":[{"type":"平静","intensity":1}]},
        "simple_report":{
          "emotion":{"type":"平静","intensity":1},
          "riskLevel":0,
          "distressLevel":0,
          "suggestions":[]
        }
      }`

	report, err := extraReport(input)
	if err != nil {
		t.Fatalf("extraReport() error = %v", err)
	}
	if len(report.SimpleReport.Emotion) != 1 || report.SimpleReport.Emotion[0] != "平静" {
		t.Fatalf("simple_report.emotion = %#v, want [平静]", report.SimpleReport.Emotion)
	}
	if len(report.Analysis.Emotion) != 1 || report.Analysis.Emotion[0].Type != "平静" {
		t.Fatalf("analysis.emotion = %#v, want object array preserved", report.Analysis.Emotion)
	}
}
