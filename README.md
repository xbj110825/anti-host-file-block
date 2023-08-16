# hosts-unblocker

`hosts-unblocker` 允许用户正常访问通过 `hosts` 文件被屏蔽的域名，而无需修改或恢复 `hosts` 文件本身。

## 快速开始

1. **编译**:

```bash
go get
go build .
```

2. **运行**

```bash
./hosts-unblocker
```

现在，你可以打开浏览器并正常访问之前被屏蔽的域名。

## 实现机制
本工具在本地运行了一个 HTTPS 代理服务器，承接 hosts 文件中重定向到本地的流量，它会解析传入连接的 TLS ClientHello 消息中的 SNI 字段来识别目标域名。该代理将忽略 hosts 文件，始终查询指定的 DNS 服务器，来获取正确的 IP 地址。

## 注意

运行代理服务器时，请确保遵守相关规定。
