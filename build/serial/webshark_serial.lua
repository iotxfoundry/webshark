-- webshark 串口分发器: USER0 帧逐帧嗅探协议并分发（采集层协议无关, 深度解析全在插件侧）。
-- 分发次序: FT1.2 → DL/T 645 → DL/T 698.45 → Modbus RTU（CRC16(0xA001) 过则链回内建 mbrtu）→ 兜底通用帧。
-- FT1.2 链回内建 iec60870_101(如需按 103 解析, 改下一行 dissector 名即可)。
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

-- FT1.2（IEC 60870-5-101/103 共用链路层）: 内建 iec60870_101 / iec60870_5_103 自带
-- 完整 FT1.2 成帧 + ASDU 解析, 形态匹配即整帧链回。两者链路帧形态相同, 仅 ASDU
-- 语义不同; 选哪个由 entrypoint 按 SERIAL_FT12 替换下方占位注入, 本文件不写死。
local ft12dis = Dissector.get("iec60870_101")
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
  ft12dis:call(buf, pinfo, tree)
  return true
end

-- 数据区字节 -0x33 还原为小写 hex 串（帧序）
local function un33hex(buf, off, len)
  local t = {}
  for i = 0, len - 1 do
    t[i + 1] = string.format("%02x", (buf(off + i, 1):uint() - 0x33) & 0xff)
  end
  return table.concat(t)
end

-- BCD 数字串按小数位定位显示（长度不足小数位时左补零）, 含 A-F 位视为非 BCD
local function bcddigits(hex, dec)
  if hex:find("[a-f]") then return nil end
  if dec > 0 then
    if #hex <= dec then
      return "0." .. string.rep("0", dec - #hex) .. hex
    end
    local ip = hex:sub(1, #hex - dec)
    if ip == "" then ip = "0" end
    return ip .. "." .. hex:sub(#hex - dec + 1)
  end
  return tostring(tonumber(hex))
end

-- 字节对倒序: 645 多字节数值低字节在前（还原后的帧序字节对 → 标准书写数字串）
local function revhex(hex)
  local t = {}
  for i = #hex, 1, -2 do t[#t + 1] = hex:sub(i - 1, i) end
  return table.concat(t)
end

-- 日期时间: 帧序 秒 分 时 日 月 年（各 1B BCD）
local function dtdisplay(hex)
  if #hex ~= 12 then return nil end
  local b = {}
  for i = 0, 5 do b[i] = tonumber(hex:sub(i * 2 + 1, i * 2 + 2), 16) end
  return string.format("20%02X-%02X-%02X %02X:%02X:%02X", b[5], b[4], b[3], b[2], b[1], b[0])
end

-- DL/T 645 数据标识常用子集: 键 = 标准书写序 DI3DI2DI1DI0 的 8 位 hex, 值 =
-- { 名称, 量纲, 小数位 [, "dt"=日期时间特殊格式] }。数值按还原后 BCD 全数字
-- 缩放（电能 XXXXXX.XX / 电压 XXX.X / 电流 XXX.XXX）。全表见社区解析表,
-- DI 映射子集的出处与许可记录在 docs/deployment.md「串口与 CAN」。
local ditab = {
  ["00009010"] = { "组合有功总电能", "kWh", 2 },
  ["00009011"] = { "正有功总电能", "kWh", 2 },
  ["00009001"] = { "日期时间", "", 0, "dt" },
  ["02010100"] = { "A 相电压", "V", 1 },
  ["02010101"] = { "B 相电压", "V", 1 },
  ["02010102"] = { "C 相电压", "V", 1 },
  ["03010100"] = { "A 相电流", "A", 3 },
  ["03010101"] = { "B 相电流", "A", 3 },
  ["03010102"] = { "C 相电流", "A", 3 },
}
for i = 1, 13 do
  ditab[string.format("010101%02x", i)] = { string.format("正有功费率%d电能", i), "kWh", 2 }
end

-- DL/T 645 控制码功能码（D4..D0）
local cfunc = {
  [0x11] = "读数据", [0x12] = "读后续数据", [0x14] = "写数据", [0x15] = "写地址",
  [0x16] = "修改通信速率", [0x17] = "修改密码", [0x18] = "最大需量清零",
  [0x19] = "电表清零", [0x1A] = "事件清零", [0x1C] = "输出控制",
}

-- DL/T 645-2007: 68 A0..A5 68 C L DATA CS 16, CS = 自地址域首字节（字节 1）起
-- 累加 mod 256。树级解析: 控制码 C 拆方向/从动/后续/功能码, 数据区 -0x33 还原,
-- 数据标识 DI（帧序 DI0..DI3, 还原后倒序即书写序 DI3DI2DI1DI0, 传输低字节在前）
-- 映射常用量; 未知 DI 显示 "DI=xxxxxxxx (BCD 原值)"; 校验错仍出树标 "(错)" 不丢帧。
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
  local bc = addr == "999999999999" and " (广播)" or ""
  local c = buf(8,1):uint()
  local fname = cfunc[c & 0x1F] or string.format("0x%02X", c & 0x1F)
  local t = tree:add(p, buf(), "DL/T 645-2007, 地址 " .. addr .. bc)
  t:add(buf(0,1), "起始 0x68")
  t:add(buf(1,6), "地址域 A0..A5")
  t:add(buf(7,1), "起始 0x68")
  local ct = t:add(buf(8,1), string.format("控制码 C=0x%02X (%s, %s)", c,
    (c >> 7 & 1 == 1) and "从站应答" or "主站请求", fname))
  ct:add(buf(8,1), "方向 D7=" .. (c >> 7 & 1)
    .. ((c >> 7 & 1 == 1) and " (从站→主站)" or " (主站→从站)"))
  ct:add(buf(8,1), "从动标志 D6=" .. (c >> 6 & 1))
  ct:add(buf(8,1), "有后续帧 D5=" .. (c >> 5 & 1))
  ct:add(buf(8,1), "功能码 D4..D0=" .. string.format("0x%02X", c & 0x1F) .. " (" .. fname .. ")")
  t:add(buf(9,1), "长度 L=" .. L)
  if L >= 4 then
    local wire = un33hex(buf, 10, 4)                       -- DI 帧序 DI0DI1DI2DI3
    local key = wire:sub(7,8) .. wire:sub(5,6) .. wire:sub(3,4) .. wire:sub(1,2) -- 书写序
    local ent = ditab[key]
    t:add(buf(10,4), ent and ("DI=" .. key .. " " .. ent[1]) or ("DI=" .. key .. " (BCD 原值)"))
    if L > 4 then
      local vh = un33hex(buf, 14, L - 4)
      local vt = t:add(buf(14, L - 4), "数据 " .. vh:upper() .. " (+0x33 线序)")
      if ent then
        if ent[4] == "dt" then
          local d = dtdisplay(vh)
          vt:add(buf(14, L - 4), "BCD -0x33 还原: " .. (d or "非 BCD 字节"))
        else
          local d = bcddigits(revhex(vh), ent[3]) -- 数值低字节在前, 倒序后按书写数字串读
          vt:add(buf(14, L - 4),
            d and ("BCD -0x33 还原: " .. d .. " " .. ent[2]) or "BCD -0x33 还原: 非 BCD 字节")
        end
      end
    end
  elseif L > 0 then
    t:add(buf(10, L), "数据 " .. buf(10, L):bytes():tohex():upper())
  end
  t:add(buf(n-2,1), "校验和 CS" .. (okcs and "" or " (错)"))
  t:add(buf(n-1,1), "结束 0x16")
  pinfo.cols.protocol = "DLT645"
  return true
end

-- DL/T 698.45 链路层骨架树: 68 H L(2B 小端) SA CA SA_D CA_D APDU(n) CS1 CS2 16。
-- L = 帧总长 - 2（形态一致性校验用; APDU 实长由帧长推导 n-11, 不依赖 L 语义展开）。
-- CS1 = 68..CA_D 逐字节累加 mod 256, CS2 = H..APDU 末 逐字节累加 mod 256。
-- H 控制域按位: bit7 方向（0=下行/主站, 1=上行/从动站）, bit6 存在 PC（启动站
-- 标志）, bit5..4 功能码高位, bit3..0 功能码低位。APDU 仅出原始字节（A-XDR 未解）,
-- 校验错标 "(错)" 不丢帧。
local function dlt698(buf, pinfo, tree)
  local n = buf:len()
  if n < 11 or buf(0,1):uint() ~= 0x68 or buf(n-1,1):uint() ~= 0x16 then return false end
  local L = buf(2,1):uint() + buf(3,1):uint() * 256 -- 2B 小端
  if L + 2 ~= n then return false end
  local cs1, cs2 = buf(0,1):uint(), buf(1,1):uint() -- cs1 自 68 起, cs2 自 H 起
  for i = 1, 7 do cs1 = cs1 + buf(i,1):uint() end
  for i = 2, n - 4 do cs2 = cs2 + buf(i,1):uint() end
  local ok1 = cs1 % 256 == buf(n-3,1):uint()
  local ok2 = cs2 % 256 == buf(n-2,1):uint()
  local h = buf(1,1):uint()
  local t = tree:add(p, buf(), string.format("DL/T 698.45, L=%d, APDU=%dB", L, n - 11))
  t:add(buf(0,1), "起始 0x68")
  local ht = t:add(buf(1,1), string.format("H 控制域 0x%02X (方向=%s, 存在PC=%d)", h,
    (h >> 7 & 1 == 1) and "上行(从动站)" or "下行(主站)", h >> 6 & 1))
  ht:add(buf(1,1), "方向 bit7=" .. (h >> 7 & 1)
    .. ((h >> 7 & 1 == 1) and " (上行/从动站)" or " (下行/主站)"))
  ht:add(buf(1,1), "存在PC bit6=" .. (h >> 6 & 1))
  ht:add(buf(1,1), "功能码高位 bit5..4=0x" .. string.format("%X", h >> 4 & 3))
  ht:add(buf(1,1), "功能码低位 bit3..0=0x" .. string.format("%X", h & 0x0F))
  t:add(buf(2,2), "长度 L=" .. L .. " (帧总长-2, 小端)")
  t:add(buf(4,1), "SA 源地址")
  t:add(buf(5,1), "CA")
  t:add(buf(6,1), "SA_D")
  t:add(buf(7,1), "CA_D")
  if n > 11 then
    t:add(buf(8, n - 11), "APDU (" .. (n - 11) .. "B, 骨架, A-XDR 未解) "
      .. buf(8, n - 11):bytes():tohex():upper())
  end
  t:add(buf(n-3,1), "CS1 (68..CA_D)" .. (ok1 and "" or " (错)"))
  t:add(buf(n-2,1), "CS2 (H..APDU)" .. (ok2 and "" or " (错)"))
  t:add(buf(n-1,1), "结束 0x16")
  pinfo.cols.protocol = "DLT698.45"
  return true
end

function p.dissector(buf, pinfo, tree)
  -- 分发次序: 结构特征强的先行, Modbus CRC 兜底, 全不中落通用帧。
  -- 次序依据: FT1.2/645/698 均以 68 开头但结构互斥（FT1.2 变长要求第 4 字节为
  -- 0x68, 645 要求第 8 字节为 0x68, 698 要求 L=帧总长-2 的 2B 小端长度域）,
  -- 形态校验串行排除, 误认概率仅存于畸形帧（校验位上标 "(错)" 留证据）。
  if ft12(buf, pinfo, tree) then return end
  if dlt645(buf, pinfo, tree) then return end
  if dlt698(buf, pinfo, tree) then return end
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
