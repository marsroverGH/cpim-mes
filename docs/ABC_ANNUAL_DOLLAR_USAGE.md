# ABC Analysis — Annual Dollar Usage

## Purpose

ABC inventory classification now follows the CPIM-style annual dollar usage basis instead of current inventory value.

**Annual Dollar Usage = rolling 12-month physical ISSUE quantity × current Standard Cost**

The ranking basis is economic usage/consumption, not how much stock happens to be on hand on the analysis date.

## Usage window

The default analysis end date is the application's business date (`eco_business_date(now())`, Asia/Tokyo fallback). The window is the 12 calendar months ending on that date, inclusive. The API also accepts `?asOf=YYYY-MM-DD` for reproducible historical analysis.

Only `inventory_txns.txn_type='ISSUE'` counts as usage. The following are intentionally excluded:

- RECEIPT
- ADJUST, including cycle-count corrections
- NCR RETURN_TO_SUPPLIER and SCRAP adjustments
- RESERVE / UNRESERVE

This prevents corrections and quality dispositions from inflating normal annual consumption.

## Cost basis

Current `items.standard_cost` is used as the unit-cost basis because historical issue-level unit cost is not stored in the existing ledger. The API explicitly returns `costBasis=STANDARD_COST` and `usageBasis=ISSUE`.

Current on-hand and on-hand value remain visible as reference information, but they do not affect ABC ranking.

## Classification policy

Rows are sorted by annual dollar usage descending. Existing policy thresholds are retained:

- A: cumulative annual dollar usage <= 70%
- B: cumulative annual dollar usage <= 90%
- C: remainder

If the entire 12-month window has zero usage value, all items are classified C rather than being incorrectly promoted to A.

## Cycle Count integration

Cycle Count scheduling calls the same `ABCService.Run()` method, so its frequency automatically follows the new annual-dollar-usage classification:

- A: 7 days
- B: 30 days
- C: 90 days

Items with zero on-hand are still skipped when generating a physical count schedule.

## Database migration

`0028_abc_annual_dollar_usage.sql` adds a partial index over physical ISSUE transactions to accelerate rolling-window analysis:

`idx_inventory_txns_abc_issue_period`

No historic inventory data is rewritten.
