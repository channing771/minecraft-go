# 局域网专用服务端

`mcgod` 是 M3C 的无渲染 TCP 专用服务端。`--max-players` 只接受 `1..8`，默认 8。启动一个新世界或打开已有世界：

```sh
go run ./cmd/mcgod --listen :25565 --world worlds/lan --seed 42 --max-players 8
```

同一局域网中的客户端可连接服务器所在机器的私有地址：

```sh
go run ./cmd/mcgo --connect 192.168.x.x:25565 --name Chen
```

如果只允许本机连接，请明确监听 loopback 地址：

```sh
go run ./cmd/mcgod --listen 127.0.0.1:25565 --world worlds/local --max-players 8
```

两个客户端人工验收时，在两个终端分别执行：

```sh
go run ./cmd/mcgo --connect 127.0.0.1:25565 --name 星河
go run ./cmd/mcgo --connect 127.0.0.1:25565 --name 月海
```

客户端首次运行会在本机 profile 中创建稳定 UUIDv4；之后改变 `--name` 只更新这个 ID 的显示名，不会创建新玩家。相同 PlayerID 同时登录会被拒绝；不同 PlayerID 可使用相同显示名，因此昵称不是身份凭据，也不保证唯一。

客户端正常断线后，服务端会保存该玩家的最终位置和安全位置；随后使用同一 PlayerID 重连会恢复存档。按 `Ctrl-C` 或发送 SIGTERM 正常关服时，服务端先停止接纳连接，再按客户端优先顺序断开会话，并刷写玩家与区块存档。不要通过强制杀进程来替代正常关服。

按 `Ctrl-C`（SIGINT）或向进程发送 SIGTERM 可正常关闭服务端。它会停止接纳连接、刷写玩家和区块存档并释放 `world.lock`。备份前请先执行上述正常关服步骤，等待进程退出后再复制整个世界目录；不要在服务端运行时复制存档文件。

> 安全警告：M3C 没有认证，也没有加密。PlayerID 与昵称都由客户端声明，任何能连到监听地址的人都可尝试冒用身份或读取/篡改明文流量。仅可用于可信局域网；不要在路由器上做端口映射，也不要暴露到公网。
