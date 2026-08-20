package service

import (
	"fmt"
	"strings"

	"github.com/cpim-mes/backend/internal/domain"
)

func ValidateSupplierNCRDisposition(role domain.Role, disposition string, quantity, affectedQty float64) (string, error) {
	d := strings.ToUpper(strings.TrimSpace(disposition))
	switch d {
	case "RETURN_TO_SUPPLIER", "SCRAP", "REWORK", "USE_AS_IS":
	default:
		return "", fmt.Errorf("invalid disposition %q", disposition)
	}
	if role != domain.RolePlanner && role != domain.RoleAdmin {
		return "", fmt.Errorf("NCR disposition requires planner/admin")
	}
	if d == "USE_AS_IS" && role != domain.RoleAdmin {
		return "", fmt.Errorf("USE_AS_IS requires admin")
	}
	if quantity <= 0 {
		return "", fmt.Errorf("quantity must be > 0")
	}
	if affectedQty > 0 && quantity > affectedQty+1e-6 {
		return "", fmt.Errorf("disposition quantity exceeds affected quantity")
	}
	return d, nil
}
