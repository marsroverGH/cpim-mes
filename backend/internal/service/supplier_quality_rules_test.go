package service

import (
	"testing"

	"github.com/cpim-mes/backend/internal/domain"
)

func TestValidateSupplierNCRDisposition(t *testing.T) {
	tests := []struct {
		name     string
		role     domain.Role
		disp     string
		qty      float64
		affected float64
		wantErr  bool
	}{
		{"planner return", domain.RolePlanner, "RETURN_TO_SUPPLIER", 10, 10, false},
		{"planner scrap", domain.RolePlanner, "SCRAP", 5, 10, false},
		{"planner rework", domain.RolePlanner, "REWORK", 10, 10, false},
		{"planner cannot concession", domain.RolePlanner, "USE_AS_IS", 10, 10, true},
		{"admin concession", domain.RoleAdmin, "USE_AS_IS", 10, 10, false},
		{"operator cannot disposition", domain.RoleOperator, "SCRAP", 1, 1, true},
		{"over affected", domain.RoleAdmin, "SCRAP", 11, 10, true},
		{"zero quantity", domain.RoleAdmin, "SCRAP", 0, 10, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ValidateSupplierNCRDisposition(tt.role, tt.disp, tt.qty, tt.affected)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
		})
	}
}
