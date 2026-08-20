package service

import "testing"

func TestValidateFinishedGoodsAgainstFinalOperation(t *testing.T) {
	tests := []struct {
		name                          string
		planned, received, final, now float64
		wantErr                       bool
	}{
		{"first receipt within final actual", 100, 0, 20, 20, false},
		{"incremental receipt within final actual", 100, 10, 20, 10, false},
		{"cannot exceed final actual", 100, 0, 20, 21, true},
		{"cannot cumulatively exceed final actual", 100, 15, 20, 6, true},
		{"cannot exceed WO planned", 100, 90, 100, 11, true},
		{"final actual cannot exceed planned", 100, 0, 101, 1, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateFinishedGoodsAgainstFinalOperation(tt.planned, tt.received, tt.final, tt.now)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error=%v wantErr=%v", err, tt.wantErr)
			}
		})
	}
}

func TestCalcOperationCumulative(t *testing.T) {
	tests := []struct {
		name                                 string
		planned, current, reported, received float64
		isFinal                              bool
		wantDelta                            float64
		wantStatus                           string
		wantErr                              bool
	}{
		{"partial first report", 100, 0, 20, 0, true, 20, "IN_PROGRESS", false},
		{"partial next report", 100, 20, 40, 10, true, 20, "IN_PROGRESS", false},
		{"full report", 100, 80, 100, 80, true, 20, "COMPLETED", false},
		{"cannot decrease", 100, 40, 30, 20, true, 0, "", true},
		{"final cannot drop below receipts", 100, 0, 10, 20, true, 0, "", true},
		{"intermediate ignores WO receipt gate", 100, 0, 10, 20, false, 10, "IN_PROGRESS", false},
		{"no duplicate cumulative report", 100, 20, 20, 0, false, 0, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			delta, status, err := CalcOperationCumulative(tt.planned, tt.current, tt.reported, tt.received, tt.isFinal)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error=%v wantErr=%v", err, tt.wantErr)
			}
			if !tt.wantErr && (delta != tt.wantDelta || status != tt.wantStatus) {
				t.Fatalf("delta/status=%v/%s want %v/%s", delta, status, tt.wantDelta, tt.wantStatus)
			}
		})
	}
}
