package database

import "strings"

type backupTable struct {
	name    string
	columns []string
	keys    []string
}

// Keep parents before children for bootstrap; only these application tables replicate.
var backupTables = []backupTable{
	{"users", fields("id,email,display_name,password_hash,password_salt,password_iterations,created_at"), fields("id")},
	{"sessions", fields("token_hash,user_id,expires_at,created_at"), fields("token_hash")},
	{"api_tokens", fields("id,user_id,name,token_hash,scope,expires_at,created_at"), fields("id")},
	{"mutation_requests", fields("user_id,operation,request_key,fingerprint,status,response,created_at"), fields("user_id,operation,request_key")},
	{"operation_logs", fields("id,user_id,operation,request_key,response,created_at"), fields("id")},
	{"assets", fields("id,user_id,name,category,code,amount,quantity,currency,annual_rate,investment_strategy,investment_amount,note,created_at,version,archived_at"), fields("id")},
	{"income_settings", fields("user_id,monthly_salary,monthly_savings,annual_bonus,updated_at,compensation,version"), fields("user_id")},
	{"retirement_goal_items", fields("id,user_id,name,category,amount,currency,created_at,version"), fields("id")},
	{"exchange_rates", fields("currency,cny_rate,rate_date,updated_at"), fields("currency")},
	{"exchange_rate_history", fields("id,currency,cny_rate,rate_date,source,fetched_at"), fields("id")},
	{"market_returns", fields("id,category,code,lookback_days,calculation_date,annual_rate,period_return,requested_days,actual_days,history_limited,start_date,end_date,source,calculated_at"), fields("id")},
	{"asset_history", fields("id,user_id,snapshot_date,total_cny,trigger,rate_date,created_at,updated_at"), fields("id")},
	{"market_daily_prices", fields("category,code,price_date,price,return_price,income,annual_rate,currency,source,fetched_at"), fields("category,code,price_date")},
	{"market_quotes", fields("category,code,price,currency,price_date,source,fetched_at"), fields("category,code")},
	{"market_sync_state", fields("category,code,covered_from,covered_to,inception_known,input_version,observation_count,checked_at,full_checked_at,last_success,last_error,retry_after"), fields("category,code")},
	{"market_return_cache", fields("category,code,lookback_days,calculation_date,input_version,payload,calculated_at"), fields("category,code,lookback_days,calculation_date")},
	{"asset_daily_snapshots", fields("user_id,asset_id,snapshot_date,amount,quantity,currency,annual_rate,price_date,source,fetched_at"), fields("user_id,asset_id,snapshot_date")},
	{"market_refresh_runs", fields("run_date,slot,job_state,started_at,finished_at,success_count,failure_count,last_error"), fields("run_date,slot")},
}

func fields(s string) []string   { return strings.Split(s, ",") }
func quoteIdent(s string) string { return "`" + strings.ReplaceAll(s, "`", "``") + "`" }

func findBackupTable(name string) (backupTable, bool) {
	for _, table := range backupTables {
		if table.name == name {
			return table, true
		}
	}
	return backupTable{}, false
}
