# Forecast Version / Forecast Consumption

Migration `0025_forecast_version_consumption.sql` replaces unversioned generated forecasts with explicit forecast versions.

## Version lifecycle

A statistical forecast can be previewed without persistence. When `saveAsVersion=true`, the future values are stored in `forecast_runs` and `forecast_values` as a new `DRAFT` version for the selected item/scenario. Versions are numbered independently per `(item, scenario)`.

Only a `DRAFT` version can become `ACTIVE`. Activating it archives the prior ACTIVE version for the same item/scenario. `forecast_values` become immutable once the run is ACTIVE or ARCHIVED. The database also enforces one ACTIVE version per item/scenario.

`asOfDate` separates historical orders used to train the statistical model from current/future customer orders used for consumption. Orders before the as-of date are training history; forecast buckets generated after that cut-off are consumed by customer orders falling in each bucket.

## Consumption rule

For each forecast bucket:

- `consumedForecast = min(forecastQty, orderQty)`
- `remainingForecast = max(forecastQty - orderQty, 0)`
- `orderAboveForecast = max(orderQty - forecastQty, 0)`
- `totalDemand = orderQty + remainingForecast = max(forecastQty, orderQty)`

Example: Forecast 100 and customer orders 60 results in consumed 60, remaining forecast 40, and total demand 100 rather than 160.

## MPS publishing

Only an ACTIVE forecast version can be explicitly published to MPS. Publishing writes `totalDemand` into `mps_entries.planned` for each bucket and stores `source_forecast_run_id` plus `demand_basis='FORECAST_CONSUMPTION'`. Existing `released` quantities are preserved.

A manual MPS edit clears this provenance and sets `demand_basis='MANUAL'`.

## Legacy forecasts

Existing `demand_forecasts(source='FORECAST')` rows are retained for audit and copied into an ARCHIVED `LEGACY` version during migration. New manual FORECAST demand creation is rejected by both Backend and DB; generated forecasts must use version management. Forecast-derived MPS rows also retain and validate the ACTIVE forecast run used as provenance.
