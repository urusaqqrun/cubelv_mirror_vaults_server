#!/usr/bin/env python3
"""
Serper API MCP Server
提供網路搜尋功能，使用 Serper API 進行 Google 搜尋
"""

import os
import json
import asyncio
import requests
from mcp.server import Server
from mcp.server.stdio import stdio_server
from mcp.types import Tool, TextContent

# 從環境變數獲取配置
SERPER_API_KEY = os.getenv('SERPER_API_KEY', '')

# 創建 MCP Server
app = Server("serper-search")


@app.list_tools()
async def list_tools() -> list[Tool]:
    """列出可用的工具"""
    return [
        Tool(
            name="web_search",
            description="使用 Serper API 進行網路搜尋，返回 Google 搜尋結果。支援多種搜尋類型（網頁、圖片、新聞等）",
            inputSchema={
                "type": "object",
                "properties": {
                    "q": {
                        "type": "string",
                        "description": "搜尋查詢字串"
                    },
                    "num": {
                        "type": "integer",
                        "description": "返回結果數量（預設 10）",
                        "default": 10
                    },
                    "type": {
                        "type": "string",
                        "description": "搜尋類型：search（網頁）、images（圖片）、news（新聞）",
                        "enum": ["search", "images", "news"],
                        "default": "search"
                    },
                    "lang": {
                        "type": "string",
                        "description": "搜尋語言（如：zh-tw, en, ja）",
                        "default": "zh-tw"
                    }
                },
                "required": ["q"]
            }
        )
    ]


@app.call_tool()
async def call_tool(name: str, arguments: dict) -> list[TextContent]:
    """執行工具調用"""
    if name != "web_search":
        raise ValueError(f"未知的工具: {name}")
    
    # 檢查 API Key
    if not SERPER_API_KEY:
        return [TextContent(
            type="text",
            text="錯誤：未設置 SERPER_API_KEY 環境變數。請到 https://serper.dev 註冊並獲取 API Key。"
        )]
    
    # 提取參數（移除內部參數）
    query = arguments.get("q", "")
    num_results = arguments.get("num", 10)
    search_type = arguments.get("type", "search")
    lang = arguments.get("lang", "zh-tw")
    # 移除內部參數（不傳遞給 Serper API）
    arguments.pop("_authorization", None)
    
    if not query:
        return [TextContent(type="text", text="錯誤：查詢字串不能為空")]
    
    try:
        # 調用 Serper API
        url = "https://google.serper.dev/search"
        headers = {
            "X-API-KEY": SERPER_API_KEY,
            "Content-Type": "application/json"
        }
        payload = {
            "q": query,
            "num": num_results,
            "gl": lang.split("-")[0] if "-" in lang else lang,
            "hl": lang
        }
        
        # 根據搜尋類型調整 URL
        if search_type == "images":
            url = "https://google.serper.dev/images"
        elif search_type == "news":
            url = "https://google.serper.dev/news"
        
        # 發送請求
        response = requests.post(url, headers=headers, json=payload, timeout=30)
        response.raise_for_status()
        
        data = response.json()
        
        # 格式化結果
        result_text = f"搜尋查詢：{query}\n"
        result_text += f"找到約 {data.get('searchInformation', {}).get('totalResults', 'N/A')} 筆結果\n\n"
        
        # 處理搜尋結果
        if search_type == "search":
            organic_results = data.get("organic", [])
            for i, result in enumerate(organic_results[:num_results], 1):
                result_text += f"{i}. {result.get('title', 'N/A')}\n"
                result_text += f"   連結：{result.get('link', 'N/A')}\n"
                result_text += f"   摘要：{result.get('snippet', 'N/A')}\n\n"
        
        elif search_type == "images":
            image_results = data.get("images", [])
            for i, result in enumerate(image_results[:num_results], 1):
                result_text += f"{i}. {result.get('title', 'N/A')}\n"
                result_text += f"   圖片：{result.get('imageUrl', 'N/A')}\n"
                result_text += f"   來源：{result.get('link', 'N/A')}\n\n"
        
        elif search_type == "news":
            news_results = data.get("news", [])
            for i, result in enumerate(news_results[:num_results], 1):
                result_text += f"{i}. {result.get('title', 'N/A')}\n"
                result_text += f"   來源：{result.get('source', 'N/A')}\n"
                result_text += f"   連結：{result.get('link', 'N/A')}\n"
                result_text += f"   時間：{result.get('date', 'N/A')}\n\n"
        
        return [TextContent(type="text", text=result_text)]
    
    except requests.exceptions.RequestException as e:
        error_msg = f"Serper API 調用失敗: {str(e)}"
        if hasattr(e, 'response') and e.response is not None:
            try:
                error_data = e.response.json()
                error_msg += f"\n錯誤詳情: {json.dumps(error_data, ensure_ascii=False)}"
            except:
                error_msg += f"\n回應狀態碼: {e.response.status_code}"
        return [TextContent(type="text", text=error_msg)]
    
    except Exception as e:
        return [TextContent(type="text", text=f"發生未預期的錯誤: {str(e)}")]


async def main():
    """主函數"""
    async with stdio_server() as (read_stream, write_stream):
        await app.run(read_stream, write_stream, app.create_initialization_options())


if __name__ == "__main__":
    asyncio.run(main())
