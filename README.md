# EXT

EXT 是 CLIProxyAPI（CPA）的用量、限额与会话审计扩展。它通过原生插件接入 CLIProxyAPI Management Center（CPAMC），在侧栏提供独立的管理入口。

## 功能

- **用量总览**显示请求数、Token、估算费用和缓存命中率，支持按北京时间筛选。
- **密钥限制**会同步 CPA 的客户端密钥，支持按密钥设置周限额、总限额、每周重置时间和可调用模型。
- **请求明细**记录每次调用的模型、Token、计价状态、耗时和结果。
- **会话审计**支持查看请求、回复和工具调用。超出内联预览上限的归档可在登录后下载。

## 快速开始

需要 Linux、Docker Engine、Docker Compose 和 OpenSSL。当前 Compose 配置固定使用 CPA v8.1.0 和 CPAMC v1.28.3。

```bash
git clone https://github.com/2Wren/EXT.git
cd EXT
bash deploy/init.sh
sudo docker compose -p cpa-ext up -d --build
```

1. 打开 `http://127.0.0.1:8482/management.html`，使用初始化生成的 `.env` 中的 `CPA_MANAGEMENT_KEY` 登录。
2. 在 CPAMC 配置上游凭证并创建客户端密钥。随后从侧栏打开 **EXT**，使用 `ADMIN_PASSWORD` 登录。
3. 将客户端指向 EXT 网关。OpenAI 兼容接口的基础地址为 `http://127.0.0.1:8480/v1`，认证使用在 CPA 创建的客户端密钥。远程部署可通过 SSH 隧道或反向代理访问回环端口。

## 运行说明

EXT 在转发前检查密钥限制，从 CPA 的用量队列采集记录并计价。金额基于公开价格目录和部署方设置的费率。SQLite 账本和会话归档保存在 `state/`。

部署步骤、端口分工、备份和回退说明见[部署文档](deploy/README.md)。

## 相关项目

- [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI)
- [CLIProxyAPI Management Center](https://github.com/router-for-me/Cli-Proxy-API-Management-Center)
