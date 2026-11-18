-- webshark Lua 主入口(sharkd 全局 init.lua 仅一行 bootstrap 指向本文件,
-- 全部 Lua 配置/脚本收敛于 /opt/webshark/lua/)
dofile("/opt/webshark/lua/webshark_serial.lua")
