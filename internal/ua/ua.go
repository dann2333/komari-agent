// Package ua 给所有出站 HTTPS 请求一个统一的 User-Agent。
//
// 版本对应 Ubuntu 22.04 自带的 curl：服务器上最常见的非浏览器客户端，和内置根证书、
// TLS 指纹一起构成"一台普通服务器上的 curl"这个身份。要换就整体换，别只改这一项。
package ua

const UserAgent = "curl/7.81.0"
