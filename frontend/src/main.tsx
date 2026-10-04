// 前端启动入口：加载全局样式并将资产仪表盘挂载到页面根节点。

import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import Dashboard from "./Dashboard";
import "./globals.css";

createRoot(document.getElementById("root")!).render(<StrictMode><Dashboard /></StrictMode>);
