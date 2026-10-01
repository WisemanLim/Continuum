package disk

import (
	"context"
	"testing"
)

const ataSample = `{"device":{"name":"/dev/sda"},"model_name":"WDC WD40EFRX","serial_number":"WD-123",
"smart_status":{"passed":true},"temperature":{"current":34},"power_on_time":{"hours":12000},
"ata_smart_attributes":{"table":[{"id":5,"name":"Reallocated_Sector_Ct","value":200,"worst":200,"thresh":140,"when_failed":"","raw":{"string":"0"}},
{"id":197,"name":"Current_Pending_Sector","value":100,"worst":100,"thresh":0,"when_failed":"now","raw":{"string":"8"}}]}}`

const nvmeSample = `{"device":{"name":"/dev/nvme0"},"model_name":"Samsung 990","smart_status":{"passed":false},"temperature":{"current":61},
"nvme_smart_health_information_log":{"critical_warning":4,"percentage_used":7,"media_errors":0}}`

func TestParseSmartctl(t *testing.T) {
	s, err := ParseSmartctl([]byte(ataSample))
	if err != nil {
		t.Fatal(err)
	}
	if s.Status != "PASSED" || *s.Temperature != 34 || *s.PowerOnH != 12000 || len(s.Attributes) != 2 || !s.Attributes[1].Failed || s.Attributes[0].Failed {
		t.Fatalf("ata: %+v", s)
	}
	s, err = ParseSmartctl([]byte(nvmeSample))
	if err != nil || s.Status != "FAILING" || *s.Passed || len(s.Attributes) != 3 {
		t.Fatalf("nvme: %+v %v", s, err)
	}
	if _, err := ParseSmartctl([]byte(`{"smartctl":{"messages":[{"string":"Permission denied"}]}}`)); err == nil {
		t.Fatal("empty output must error")
	}
}

func TestReadSmartNeverFails(t *testing.T) {
	s := ReadSmart(context.Background(), t.TempDir()) // folder → its OS volume
	if s.Source == "" || s.Status == "" || s.CheckedAt == "" || s.Attributes == nil {
		t.Fatalf("smart: %+v", s)
	}
	t.Logf("host SMART: source=%s status=%s note=%s", s.Source, s.Status, s.Note)
}

func TestRunBench(t *testing.T) {
	r, err := RunBench(context.Background(), t.TempDir(), 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	if r.SeqWriteMBs <= 0 || r.SeqReadMBs <= 0 || r.RandReadIOPS <= 0 {
		t.Fatalf("bench: %+v", r)
	}
	t.Logf("bench 8MiB: write %.0f MB/s read %.0f MB/s rand %.0f IOPS bypass=%v", r.SeqWriteMBs, r.SeqReadMBs, r.RandReadIOPS, r.CacheBypass)
}
