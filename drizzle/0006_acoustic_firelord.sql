CREATE TABLE `exchange_rate_history` (
	`id` integer PRIMARY KEY AUTOINCREMENT NOT NULL,
	`currency` text NOT NULL,
	`cny_rate` real NOT NULL,
	`rate_date` text NOT NULL,
	`source` text NOT NULL,
	`fetched_at` text NOT NULL
);
--> statement-breakpoint
CREATE UNIQUE INDEX `exchange_rate_history_currency_date_unique` ON `exchange_rate_history` (`currency`,`rate_date`);--> statement-breakpoint
CREATE INDEX `exchange_rate_history_lookup_idx` ON `exchange_rate_history` (`currency`,`rate_date`);