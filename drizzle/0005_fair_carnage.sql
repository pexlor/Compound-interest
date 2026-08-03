CREATE TABLE `market_returns` (
	`id` integer PRIMARY KEY AUTOINCREMENT NOT NULL,
	`category` text NOT NULL,
	`code` text NOT NULL,
	`lookback_days` integer NOT NULL,
	`calculation_date` text NOT NULL,
	`annual_rate` real NOT NULL,
	`period_return` real NOT NULL,
	`requested_days` integer NOT NULL,
	`actual_days` integer NOT NULL,
	`history_limited` integer DEFAULT false NOT NULL,
	`start_date` text NOT NULL,
	`end_date` text NOT NULL,
	`source` text NOT NULL,
	`calculated_at` text DEFAULT CURRENT_TIMESTAMP NOT NULL
);
--> statement-breakpoint
CREATE UNIQUE INDEX `market_returns_key_date_unique` ON `market_returns` (`category`,`code`,`lookback_days`,`calculation_date`);