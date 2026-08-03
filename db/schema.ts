import { sql } from "drizzle-orm";
import { index, integer, real, sqliteTable, text, uniqueIndex } from "drizzle-orm/sqlite-core";

export const users = sqliteTable("users", {
  id: integer("id").primaryKey({ autoIncrement: true }),
  email: text("email").notNull().unique(),
  displayName: text("display_name").notNull(),
  passwordHash: text("password_hash").notNull(),
  passwordSalt: text("password_salt").notNull(),
  passwordIterations: integer("password_iterations").notNull().default(210000),
  createdAt: text("created_at").notNull().default(sql`CURRENT_TIMESTAMP`),
});

export const sessions = sqliteTable("sessions", {
  tokenHash: text("token_hash").primaryKey(),
  userId: integer("user_id").notNull().references(() => users.id, { onDelete: "cascade" }),
  expiresAt: integer("expires_at").notNull(),
  createdAt: text("created_at").notNull().default(sql`CURRENT_TIMESTAMP`),
});

export const assets = sqliteTable("assets", {
  id: integer("id").primaryKey({ autoIncrement: true }),
  userId: integer("user_id").references(() => users.id, { onDelete: "cascade" }),
  name: text("name").notNull(),
  category: text("category").notNull(),
  code: text("code"),
  amount: integer("amount").notNull(),
  currency: text("currency").notNull().default("CNY"),
  annualRate: real("annual_rate").notNull().default(0),
  investmentStrategy: text("investment_strategy").notNull().default("none"),
  investmentAmount: integer("investment_amount"),
  note: text("note").notNull().default(""),
  createdAt: text("created_at").notNull().default(sql`CURRENT_TIMESTAMP`),
});

export const exchangeRates = sqliteTable("exchange_rates", {
  currency: text("currency").primaryKey(),
  cnyRate: real("cny_rate").notNull(),
  rateDate: text("rate_date").notNull(),
  updatedAt: text("updated_at").notNull().default(sql`CURRENT_TIMESTAMP`),
});

export const exchangeRateHistory = sqliteTable("exchange_rate_history", {
  id: integer("id").primaryKey({ autoIncrement: true }),
  currency: text("currency").notNull(),
  cnyRate: real("cny_rate").notNull(),
  rateDate: text("rate_date").notNull(),
  source: text("source").notNull(),
  fetchedAt: text("fetched_at").notNull(),
}, (table) => [
  uniqueIndex("exchange_rate_history_currency_date_unique").on(table.currency, table.rateDate),
  index("exchange_rate_history_lookup_idx").on(table.currency, table.rateDate),
]);

export const assetHistory = sqliteTable("asset_history", {
  id: integer("id").primaryKey({ autoIncrement: true }),
  userId: integer("user_id").notNull().references(() => users.id, { onDelete: "cascade" }),
  snapshotDate: text("snapshot_date").notNull(),
  totalCny: integer("total_cny").notNull(),
  trigger: text("trigger").notNull(),
  rateDate: text("rate_date"),
  createdAt: text("created_at").notNull().default(sql`CURRENT_TIMESTAMP`),
  updatedAt: text("updated_at").notNull().default(sql`CURRENT_TIMESTAMP`),
}, (table) => [
  uniqueIndex("asset_history_user_date_unique").on(table.userId, table.snapshotDate),
]);

export const marketReturns = sqliteTable("market_returns", {
  id: integer("id").primaryKey({ autoIncrement: true }),
  category: text("category").notNull(),
  code: text("code").notNull(),
  lookbackDays: integer("lookback_days").notNull(),
  calculationDate: text("calculation_date").notNull(),
  annualRate: real("annual_rate").notNull(),
  periodReturn: real("period_return").notNull(),
  requestedDays: integer("requested_days").notNull(),
  actualDays: integer("actual_days").notNull(),
  historyLimited: integer("history_limited", { mode: "boolean" }).notNull().default(false),
  startDate: text("start_date").notNull(),
  endDate: text("end_date").notNull(),
  source: text("source").notNull(),
  calculatedAt: text("calculated_at").notNull().default(sql`CURRENT_TIMESTAMP`),
}, (table) => [
  uniqueIndex("market_returns_key_date_unique").on(table.category, table.code, table.lookbackDays, table.calculationDate),
]);
