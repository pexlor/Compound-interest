CREATE TABLE `income_settings` (
	`user_id` integer PRIMARY KEY NOT NULL,
	`monthly_salary` integer DEFAULT 0 NOT NULL,
	`monthly_savings` integer DEFAULT 0 NOT NULL,
	`updated_at` text DEFAULT CURRENT_TIMESTAMP NOT NULL,
	FOREIGN KEY (`user_id`) REFERENCES `users`(`id`) ON UPDATE no action ON DELETE cascade
);
