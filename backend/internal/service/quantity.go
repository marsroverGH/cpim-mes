package service

import "github.com/shopspring/decimal"

// Quantity arithmetic on the main quantity paths (MRP netting and lot sizing,
// the inventory ledger and FIFO, work order BOM requirements, reservations and
// completions, PO receipts, sales order allocation and shipment) is done in
// decimal. Domain structs and the API keep float64, so values are converted at
// the boundary:
//
//   - dq turns a float64 into the decimal a person would write. It uses the
//     shortest representation that round-trips (0.1 -> 0.1, not
//     0.1000000000000000055...), so values read from the database or the API
//     are taken exactly as they were written.
//   - fq turns a decimal back into float64 after rounding it to QuantityScale
//     places. A float64 holds such values to ~15 significant digits, so the
//     float that leaves this package prints (and is stored) as that decimal.
//
// Inside a calculation, keep the decimal and convert once at the end; chaining
// float64 operations reintroduces the binary artefacts this file exists to
// remove.

func dq(x float64) decimal.Decimal { return decimal.NewFromFloat(x) }

func fq(d decimal.Decimal) float64 {
	f, _ := d.Round(QuantityScale).Float64()
	return f
}

// roundQtyDec rounds to QuantityScale places, half away from zero (the same
// rule as PostgreSQL numeric rounding).
func roundQtyDec(d decimal.Decimal) decimal.Decimal { return d.Round(QuantityScale) }

// addQty returns a+b computed in decimal.
func addQty(a, b float64) float64 { return fq(dq(a).Add(dq(b))) }

// subQty returns a-b computed in decimal.
func subQty(a, b float64) float64 { return fq(dq(a).Sub(dq(b))) }

// sumQty returns the decimal sum of xs.
func sumQty(xs ...float64) float64 {
	total := decimal.Zero
	for _, x := range xs {
		total = total.Add(dq(x))
	}
	return fq(total)
}

// qtyEpsilon is the tolerance the database guards use when comparing
// quantities (for example the reservation and lot balance checks).
var qtyEpsilon = decimal.New(1, -6)
