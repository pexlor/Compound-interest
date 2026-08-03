CREATE TABLE `asset_history` (
	`id` integer PRIMARY KEY AUTOINCREMENT NOT NULL,
	`user_id` integer NOT NULL,
	`snapshot_date` text NOT NULL,
	`total_cny` integer NOT NULL,
	`trigger` text NOT NULL,
	`rate_date` text,
	`created_at` text DEFAULT CURRENT_TIMESTAMP NOT NULL,
	`updated_at` text DEFAULT CURRENT_TIMESTAMP NOT NULL,
	FOREIGN KEY (`user_id`) REFERENCES `users`(`id`) ON UPDATE no action ON DELETE cascade
);
--> statement-breakpoint
CREATE UNIQUE INDEX `asset_history_user_date_unique` ON `asset_history` (`user_id`,`snapshot_date`);--> statement-breakpoint
CREATE TABLE `exchange_rates` (
	`currency` text PRIMARY KEY NOT NULL,
	`cny_rate` real NOT NULL,
	`rate_date` text NOT NULL,
	`updated_at` text DEFAULT CURRENT_TIMESTAMP NOT NULL
);
