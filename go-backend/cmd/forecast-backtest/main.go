// forecast-backtest 从只读本地缓存运行当前持仓的1/3/5年滚动回测，不访问网络或改写账本。
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"time"

	"fulibu-go/internal/service"
	_ "github.com/mattn/go-sqlite3"
)

// main 校验数据库及用户编号，装配只读快照并输出带样本数与限制说明的JSON报告。
func main() {
	path := flag.String("db", "", "已有SQLite数据库绝对路径（只读）")
	userID := flag.Int64("user", 0, "要回测的用户ID")
	benchmarks := flag.String("benchmarks", "{}", "资产ID到基准类别的JSON映射")
	flag.Parse()
	if *path == "" || *userID < 1 {
		fmt.Fprintln(os.Stderr, "必须指定 -db 和 -user")
		os.Exit(2)
	}
	if _, err := os.Stat(*path); err != nil {
		fmt.Fprintln(os.Stderr, "数据库不存在")
		os.Exit(2)
	}
	o := service.DefaultForecastOptions()
	if err := json.Unmarshal([]byte(*benchmarks), &o.Benchmarks); err != nil {
		fmt.Fprintln(os.Stderr, "基准映射格式无效")
		os.Exit(2)
	}
	for _, class := range o.Benchmarks {
		if _, ok := service.ForecastBenchmarks()[class]; !ok {
			fmt.Fprintln(os.Stderr, "基准类别无效")
			os.Exit(2)
		}
	}
	db, err := sql.Open("sqlite3", (&url.URL{Scheme: "file", Path: *path}).String()+"?mode=ro&_query_only=1")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	var n int
	if err = db.QueryRow("SELECT COUNT(*) FROM users WHERE id=?", *userID).Scan(&n); err != nil || n != 1 {
		fmt.Fprintln(os.Stderr, "用户不存在或数据库不可读")
		os.Exit(2)
	}
	zone := time.FixedZone("Asia/Shanghai", 8*3600)
	now := time.Now().In(zone)
	at := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	in, _, _, _, err := service.LoadForecastInput(tx, *userID, o, at)
	tx.Rollback()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	reports := []service.BacktestReport{}
	for _, years := range []int{1, 3, 5} {
		o.Years = years
		report, e := service.BacktestForecast(in, o)
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			os.Exit(1)
		}
		reports = append(reports, report)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err = encoder.Encode(reports); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
