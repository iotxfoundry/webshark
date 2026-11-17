-- webshark 串口分发器: USER0 帧逐帧嗅探协议并分发（采集层协议无关, 深度解析全在插件侧）。
-- 分发次序: FT1.2 → DL/T 645 → Modbus RTU（CRC16(0xA001) 过则链回内建 mbrtu）→ 兜底通用帧。
-- 结构特征强的协议先行; 各协议仅按帧结构特征识别, 识别失败自然落下一级。
local p = Proto("webshark_serial", "Webshark 串口分发")
local mbrtu = Dissector.get("mbrtu")

-- Modbus RTU CRC16(0xA001 反射), 线序低字节在前
local function crc16ok(buf)
  local n, crc = buf:len(), 0xFFFF
  if n < 4 then return false end
  for i = 0, n - 3 do
    crc = crc ~ buf(i,1):uint()
    for _ = 1, 8 do
      if crc % 2 == 1 then crc = (crc >> 1) ~ 0xA001 else crc = crc >> 1 end
    end
  end
  return crc % 256 == buf(n-2,1):uint() and (crc >> 8) == buf(n-1,1):uint()
end

-- FT1.2（IEC 60870-5-101/102/103 共用链路层）: 内建 iec60870_101 自带完整
-- FT1.2 成帧 + ASDU 解析, 形态匹配即整帧链回（101/103 链路帧形态相同, 默认按
-- 101 解析; 103 场景可在 sharkd 侧换 iec60870_5_103）。校验交给内建。
local iec101 = Dissector.get("iec60870_101")
local function ft12(buf, pinfo, tree)
  local n = buf:len()
  local b0 = buf(0,1):uint()
  local shaped = false
  if b0 == 0x10 then
    shaped = n == 5 and buf(4,1):uint() == 0x16
  elseif b0 == 0x68 then
    shaped = n >= 6 and buf(1,1):uint() == buf(2,1):uint()
      and buf(3,1):uint() == 0x68 and buf(n-1,1):uint() == 0x16
      and n == buf(1,1):uint() + 6
  end
  if not shaped then return false end
  iec101:call(buf, pinfo, tree)
  return true
end

-- DL/T 645-2007: 68 A0..A5 68 C L DATA CS 16, CS = 自地址域首字节（字节 1）起累加 mod 256
local function dlt645(buf, pinfo, tree)
  local n = buf:len()
  if n < 12 or buf(0,1):uint() ~= 0x68 or buf(7,1):uint() ~= 0x68
    or buf(n-1,1):uint() ~= 0x16 then return false end
  local L = buf(9,1):uint()
  if n ~= L + 12 then return false end
  local sum = 0
  for i = 1, n - 3 do sum = sum + buf(i,1):uint() end -- 地址域起累加至数据域末（不含 CS）
  local okcs = sum % 256 == buf(n-2,1):uint()
  local addr = buf(1,6):bytes():tohex():upper()
  local t = tree:add(p, buf(), "DL/T 645-2007, 地址 " .. addr)
  t:add(buf(1,6), "地址域 A0..A5"); t:add(buf(7,1), "起始 0x68")
  t:add(buf(8,1), "控制码 C"); t:add(buf(9,1), "长度 L=" .. L)
  if L > 0 then t:add(buf(10, L), "数据") end
  t:add(buf(n-2,1), "校验和 CS" .. (okcs and "" or " (错)"))
  t:add(buf(n-1,1), "结束 0x16")
  pinfo.cols.protocol = "DLT645"
  return true
end

function p.dissector(buf, pinfo, tree)
  -- 分发次序: 结构特征强的先行, Modbus CRC 兜底, 全不中落通用帧（如 DL/T 698.45 走 hex 视图）
  if ft12(buf, pinfo, tree) then return end
  if dlt645(buf, pinfo, tree) then return end
  if crc16ok(buf) then
    mbrtu:call(buf, pinfo, tree)
    return
  end
  tree:add(p, buf(), "未识别串口帧 (" .. buf:len() .. "B)")
  pinfo.cols.protocol = "WS-SERIAL"
end

-- 注册到 wtap_encap USER0: 45 = WTAP_ENCAP_USER0。必须保留——entrypoint 写入的
-- user_dlts 占位行指向本 Proto 名, 二者合力才是 sharkd 把 USER0 帧交给本分发器的实证触发条件。
DissectorTable.get("wtap_encap"):add(45, p)
