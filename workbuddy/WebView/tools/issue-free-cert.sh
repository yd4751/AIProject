#!/usr/bin/env bash
# 免费 CA 证书（Let's Encrypt）签发脚本 —— DNS-01 验证，不需要备案、不需要开放 80 端口。
#
# 适用：域名正在备案、HTTP-01 被 webblock 拦截、又不想花钱买证书的场景。
# 环境：Git Bash / WSL / Linux，需先安装 acme.sh。
#
#   curl https://get.acme.sh | sh -s email=you@example.com
#   bash tools/issue-free-cert.sh --dns manual
#   bash tools/issue-free-cert.sh --dns dp          # 需 export DP_Id / DP_Key
#   bash tools/issue-free-cert.sh --dns dp --conf C:/Users/Data/Server/nginx/conf/nginx.conf
#
# 产出（默认 C:/Users/Data/Server/nginx/cert/）：
#   bighat.icu.fullchain.cer   含完整证书链，给 ssl_certificate 用
#   bighat.icu.key             私钥，给 ssl_certificate_key 用
#
# 证书 90 天有效，acme.sh 会自动续期（Windows 需配计划任务，见脚本末尾输出）。
set -euo pipefail

export MSYS2_ARG_CONV_EXCL='*'

DOMAIN='bighat.icu'
DNS_MODE='manual'
CERT_DIR='C:/Users/Data/Server/nginx/cert'
CONF_FILE=''
RELOAD_CMD=''
ACME="$HOME/.acme.sh/acme.sh"
KEYLENGTH='ec-256'

usage() {
  cat <<'EOF'
用法: bash tools/issue-free-cert.sh [选项]

  --domain NAME     域名，默认 bighat.icu
  --dns MODE        manual | dp | ali | cf，默认 manual
                    manual = 手工加一条 TXT 记录，零凭据、零 API 密钥
                    dp     = 腾讯云 DNSPod（export DP_Id / DP_Key）
                    ali    = 阿里云 DNS（export Ali_Key / Ali_Secret）
                    cf     = Cloudflare（export CF_Token / CF_Zone_ID）
  --cert-dir DIR    证书安装目录，默认 C:/Users/Data/Server/nginx/cert
  --conf FILE       可选，自动备份并替换该 nginx 配置里的两行证书路径
  --reload CMD      可选，续期后自动重载 nginx 的命令
  -h, --help        显示本说明
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    --domain)   DOMAIN="$2"; shift 2 ;;
    --dns)      DNS_MODE="$2"; shift 2 ;;
    --cert-dir) CERT_DIR="$2"; shift 2 ;;
    --conf)     CONF_FILE="$2"; shift 2 ;;
    --reload)   RELOAD_CMD="$2"; shift 2 ;;
    -h|--help)  usage; exit 0 ;;
    *) echo "未知参数: $1" >&2; usage; exit 1 ;;
  esac
done

case "$DNS_MODE" in manual|dp|ali|cf) ;; *) echo "--dns 只支持 manual/dp/ali/cf" >&2; exit 1 ;; esac

if [ ! -f "$ACME" ]; then
  echo "未找到 acme.sh（$ACME）。先安装：" >&2
  echo "  curl https://get.acme.sh | sh -s email=you@example.com" >&2
  exit 1
fi

echo "域名: $DOMAIN   验证方式: DNS-01 ($DNS_MODE)"
echo "注意: acme.sh 新版默认 CA 是 ZeroSSL，这里强制切到 Let's Encrypt。"
"$ACME" --set-default-ca --server letsencrypt

if [ "$DNS_MODE" = manual ]; then
  echo
  echo ">>> 第 1 步：生成 TXT 记录"
  echo "下面会打印一条 _acme-challenge.$DOMAIN 的 TXT 记录，先别关窗口。"
  "$ACME" --issue --dns -d "$DOMAIN" --server letsencrypt \
    --keylength "$KEYLENGTH" \
    --yes-I-know-dns-manual-mode-enough-go-ahead-please || true
  echo
  echo ">>> 请到 DNS 控制台添加上面那条 TXT 记录，等解析生效（可用 nslookup -q=TXT _acme-challenge.$DOMAIN 确认）。"
  read -r -p "生效后按回车继续第 2 步... " _
  echo ">>> 第 2 步：完成签发"
  "$ACME" --renew -d "$DOMAIN" --ecc \
    --yes-I-know-dns-manual-mode-enough-go-ahead-please
else
  case "$DNS_MODE" in
    dp)
      : "${DP_Id:?请先 export DP_Id=你的DNSPod ID}"
      : "${DP_Key:?请先 export DP_Key=你的DNSPod Token}"
      export DP_Id DP_Key
      PLUGIN='dns_dp' ;;
    ali)
      : "${Ali_Key:?请先 export Ali_Key=...}"
      : "${Ali_Secret:?请先 export Ali_Secret=...}"
      export Ali_Key Ali_Secret
      PLUGIN='dns_ali' ;;
    cf)
      : "${CF_Token:?请先 export CF_Token=...}"
      export CF_Token
      if [ -n "${CF_Zone_ID:-}" ]; then export CF_Zone_ID; fi
      PLUGIN='dns_cf' ;;
  esac
  echo "使用 DNS 插件: $PLUGIN（TXT 记录会自动添加并删除）"
  "$ACME" --issue --dns "$PLUGIN" -d "$DOMAIN" --server letsencrypt \
    --keylength "$KEYLENGTH" --dnssleep 120
fi

mkdir -p "$CERT_DIR"
FULLCHAIN="$CERT_DIR/$DOMAIN.fullchain.cer"
KEYFILE="$CERT_DIR/$DOMAIN.key"
if [ -z "$RELOAD_CMD" ]; then
  RELOAD_CMD="echo '请手动执行 nginx -s reload 使新证书生效'"
fi

echo ">>> 安装证书到 $CERT_DIR"
"$ACME" --install-cert -d "$DOMAIN" --ecc \
  --fullchain-file "$FULLCHAIN" \
  --key-file "$KEYFILE" \
  --reloadcmd "$RELOAD_CMD"

if [ -n "$CONF_FILE" ]; then
  if [ ! -f "$CONF_FILE" ]; then
    echo "找不到配置文件: $CONF_FILE" >&2
    exit 1
  fi
  BACKUP="$CONF_FILE.bak.$(date +%Y%m%d%H%M%S)"
  cp "$CONF_FILE" "$BACKUP"
  sed -i \
    -e "s|ssl_certificate[[:space:]]\+.*selfsigned\.crt;|ssl_certificate     $FULLCHAIN;|g" \
    -e "s|ssl_certificate_key[[:space:]]\+.*selfsigned\.key;|ssl_certificate_key $KEYFILE;|g" \
    "$CONF_FILE"
  echo ">>> 已替换证书路径，原配置备份在: $BACKUP"
  echo ">>> 请务必先执行 nginx -t 校验，通过后再 nginx -s reload"
else
  echo
  echo ">>> 未指定 --conf，请手动把 nginx 配置里两行改成："
  echo "    ssl_certificate     $FULLCHAIN;"
  echo "    ssl_certificate_key $KEYFILE;"
  echo "    然后执行 nginx -t && nginx -s reload"
fi

cat <<EOF

完成。接下来还有两件事（都不是花钱的事）：
  1. 证书是受信任 CA 签发的，但小程序正式版仍要等 ICP 备案完成，
     再到「开发管理 → 开发设置 → 服务器域名」登记 https://$DOMAIN:7000
  2. 证书 90 天有效。Linux 下 acme.sh 自带 cron；Windows 需加计划任务，例如：
     schtasks /Create /TN "acme-renew-$DOMAIN" /SC DAILY /ST 03:00 /F /TR "C:\\Program Files\\Git\\bin\\bash.exe -lc '~/.acme.sh/acme.sh --cron'"
     可用 acme.sh --cron --force 先演练一次。

自检： openssl s_client -connect $DOMAIN:7000 -servername $DOMAIN -showcerts
应看到 issuer 为 Let's Encrypt，且 Verify return code: 0 (ok)。
EOF
