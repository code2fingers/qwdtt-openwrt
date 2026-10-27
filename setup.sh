#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Adds a qWDTT tunnel from a qwdtt:// or wdtt:// link, or points an existing one
# at a new link. Run it after install.sh, with the link or without it:
#
#   sh <(wget -O - https://raw.githubusercontent.com/code2fingers/qwdtt-openwrt/main/setup.sh)
#
# Reading the link is the client's job (-parse-link), so this and the LuCI
# import read it the same way. It carries the connection password, which goes
# to the client and to uci on stdin, never as an argument.
#
# It speaks the router's LuCI language where that is Russian, as the installation
# docs do: the person running it is at the router, not reading the code.

set -eu

RUN_DIR=/var/run/qwdtt

LINK=
MODE=
IFACE=
DEVICE_ID=
CONFIRM=1
BRING_UP=1
WAIT=300

# Russian when LuCI is set to it, or follows the browser and has it installed.
case "$(uci -q get luci.main.lang || echo auto)" in
ru*) RU=1 ;;
auto) RU=0; [ -z "$(uci -q get luci.languages.ru)" ] || RU=1 ;;
*) RU=0 ;;
esac
L() { if [ "$RU" = 1 ]; then printf '%s' "$2"; else printf '%s' "$1"; fi; }

msg()  { printf '\033[1;32m[qwdtt]\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[qwdtt]\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31m[qwdtt]\033[0m %s\n' "$*" >&2; exit 1; }
say()  { printf '%s\n' "$*" >&2; }

usage() {
	if [ "$RU" = 1 ]; then
		cat <<EOF
Использование: setup.sh [ССЫЛКА] [-i ИНТЕРФЕЙС] [-m raw|wg|wg-direct] [-d ID] [-y] [-n] [-h]

Настраивает туннель qWDTT по ссылке qwdtt:// или wdtt://: новый или уже
существующий на роутере. Спрашивает то, что не задано параметрами ниже.
Хеши из ссылки добавляются к уже заданным у интерфейса.

  ССЫЛКА        ссылка qwdtt:// или wdtt://; если не указана, будет запрошена
  -i ИНТЕРФЕЙС  перенастроить этот интерфейс qWDTT, не спрашивая
  -m РЕЖИМ      для нового интерфейса: raw (по умолчанию), wg (WireGuard)
                или wg-direct (WireGuard без DTLS)
  -d ID         идентификатор устройства туннеля, свой у каждого туннеля
  -y            применить без подтверждения
  -n            записать настройки, но ничего не запускать
  -h            показать эту справку

Запуск на роутере от root:
  sh <(wget -O - https://raw.githubusercontent.com/code2fingers/qwdtt-openwrt/main/setup.sh)
EOF
	else
		cat <<EOF
Usage: setup.sh [LINK] [-i IFACE] [-m raw|wg|wg-direct] [-d DEVICE_ID] [-y] [-n] [-h]

Sets a qWDTT tunnel up from a qwdtt:// or wdtt:// link: a new one, or one
already on the router. Asks for whatever the options below do not give. The
link's hashes are added to the ones the interface already has.

  LINK          the qwdtt:// or wdtt:// link; asked for when not given
  -i IFACE      reconfigure this qWDTT interface instead of choosing one
  -m MODE       for a new interface: raw (default), wg (WireGuard) or
                wg-direct (WireGuard without DTLS)
  -d DEVICE_ID  the tunnel's device ID, unique per tunnel
  -y            apply without asking for confirmation
  -n            write the settings, but do not bring anything up
  -h            show this help

Run as root on the router:
  sh <(wget -O - https://raw.githubusercontent.com/code2fingers/qwdtt-openwrt/main/setup.sh)
EOF
	fi
	exit 0
}

while [ $# -gt 0 ]; do
	case "$1" in
	-i) [ $# -ge 2 ] || die "$(L '-i needs an interface name' 'для -i нужно имя интерфейса')"; IFACE=$2; shift ;;
	-m) [ $# -ge 2 ] || die "$(L '-m needs raw, wg or wg-direct' 'для -m нужен raw, wg или wg-direct')"; MODE=$2; shift ;;
	-d) [ $# -ge 2 ] || die "$(L '-d needs a device ID' 'для -d нужен идентификатор устройства')"; DEVICE_ID=$2; shift ;;
	-y) CONFIRM=0 ;;
	-n) BRING_UP=0 ;;
	-h) usage ;;
	-*) die "$(L "unknown option $1; -h for help" "неизвестный параметр $1; справка: -h")" ;;
	*) [ -z "$LINK" ] || die "$(L 'more than one link given' 'указано больше одной ссылки')"; LINK=$1 ;;
	esac
	shift
done

version=$( { apk info -v 2>/dev/null | sed -n 's/^qwdtt-client-//p'
	opkg list-installed qwdtt-client 2>/dev/null | sed -n 's/^qwdtt-client - //p'; } | head -n 1)

rule='===================================================================='
{
	printf '%s\n' "$rule"
	printf '%s\n' '        __      _____  _____ _____'
	printf '%-37s%s\n' '  __ _  \ \    / /   \|_   _|_   _|' \
		"$(if [ -n "$version" ]; then echo "v$version"; else L 'qwdtt-client not installed' 'qwdtt-client не установлен'; fi)"
	printf '%s\n' ' / _` |  \ \/\/ /| |) | | |   | |'
	printf '%-37s%s\n' ' \__, |   \_/\_/ |___/  |_|   |_|' \
		"$(L 'tunnel setup for OpenWrt' 'настройка туннеля для OpenWrt')"
	printf '%-37s%s\n' '    |_|' \
		"$(L 'from a qwdtt:// or wdtt:// link' 'по ссылке qwdtt:// или wdtt://')"
	printf '%s\n' "$rule"
} >&2

[ "$(id -u)" = 0 ] || die "$(L 'run this as root.' 'запустите от имени root.')"
command -v qwdtt-client >/dev/null 2>&1 ||
	die "$(L 'qwdtt-client is not installed: run install.sh first.' 'qwdtt-client не установлен: сначала запустите install.sh.')"
case "${MODE:-raw}" in
raw|wg|wg-direct) ;;
*) die "$(L "-m takes raw, wg or wg-direct, not $MODE" "-m принимает raw, wg или wg-direct, а не $MODE")" ;;
esac

# ask PROMPT -> the answer. Assign it: its die exits only the $(...), and set -e
# stops the script on a failed assignment but not on a failed case word.
ask() {
	printf '%s ' "$1" >&2
	read -r answer || [ -n "$answer" ] ||
		die "$(L 'no answer: the input has ended.' 'нет ответа: ввод закончился.')"
	printf '%s' "$answer"
}

tunnels() { uci show network | sed -n "s/^network\.\([^.]*\)\.proto='qwdtt'$/\1/p"; }

mode_of() { # mode_of IFACE -> raw | wg | wg-direct
	if [ "$(uci -q get "network.$1.mode")" != wireguard ]; then
		echo raw
	elif [ "$(uci -q get "network.$1.no_dtls")" = 1 ]; then
		echo wg-direct
	else
		echo wg
	fi
}

label() { # label MODE -> how the mode reads in LuCI
	case "$1" in
	raw) echo RAW ;;
	wg) echo WireGuard ;;
	wg-direct) L 'WireGuard without DTLS' 'WireGuard без DTLS' ;;
	esac
}

parse_link() { # parse_link MODE -> the link's settings as JSON, or why not
	case "$1" in
	raw) set -- -mode rawtun ;;
	wg) set -- -mode vpn ;;
	wg-direct) set -- -mode vpn -notls ;;
	esac
	printf '%s\n' "$LINK" | qwdtt-client -parse-link "$@" 2>&1
}

unreadable() { # unreadable REASON
	case "$1" in
	*parse-link*) die "$(L 'this qwdtt-client cannot read links: upgrade it with install.sh.' 'этот qwdtt-client не умеет читать ссылки: обновите его через install.sh.')" ;;
	esac
	warn "$(L "not a link this can read: $1" "ссылку не удалось разобрать: $1")"
}

# ---- 1. the link -------------------------------------------------------------
if [ -n "$LINK" ]; then
	warn "$(L 'tip: next time run it without the link and paste it when asked - a link on the command line stays in the shell history.' 'совет: в следующий раз запустите без ссылки и вставьте ее по запросу - ссылка в командной строке остается в истории оболочки.')"
	json=$(parse_link raw) || { unreadable "$json"; exit 1; }
else
	say ""
	say "$(L 'Paste the qwdtt:// or wdtt:// link of your qWDTT server.' 'Вставьте ссылку qwdtt:// или wdtt:// вашего сервера qWDTT.')"
	while :; do
		LINK=$(ask "$(L 'qWDTT-link:' 'qWDTT-ссылка:')")
		[ -n "$LINK" ] || die "$(L 'no link given.' 'ссылка не указана.')"
		json=$(parse_link raw) && break
		unreadable "$json"
	done
fi

# ---- 2. which interface -----------------------------------------------------
if [ -n "$IFACE" ]; then
	[ "$(uci -q get "network.$IFACE.proto")" = qwdtt ] ||
		die "$(L "network.$IFACE is not a qWDTT interface." "network.$IFACE - не интерфейс qWDTT.")"
	NEW=0
else
	n=0
	while uci -q get "network.qwdtt$n" >/dev/null; do n=$((n + 1)); done
	IFACE=qwdtt$n
	NEW=1

	list=$(tunnels)
	if [ -n "$list" ]; then
		say ""
		say "$(L 'qWDTT interfaces on this router:' 'Интерфейсы qWDTT на этом роутере:')"
		i=0
		for t in $list; do
			i=$((i + 1))
			printf '  %d) %-10s %-22s %s %-18s %s %-16s %s %s\n' "$i" "$t" \
				"$(label "$(mode_of "$t")")" \
				"$(L server сервер)" "$(uci -q get "network.$t.peer_host" || echo -)" \
				"$(L 'device ID' 'ID')" "$(uci -q get "network.$t.device_id" || echo -)" \
				"$(L hashes: хешей:)" "$(uci -q get "network.$t.hash" | wc -w)" >&2
		done
		say "  $((i + 1))) $(L "add a new interface, $IFACE" "добавить новый интерфейс, $IFACE")"
		while :; do
			pick=$(ask "$(L 'Reconfigure one of them, or add a new one?' 'Перенастроить один из них или добавить новый?') [$((i + 1))]:")
			[ -n "$pick" ] || pick=$((i + 1))
			case "$pick" in *[!0-9]*) pick=0 ;; esac
			if [ "$pick" -ge 1 ] && [ "$pick" -le "$i" ]; then
				IFACE=$(echo "$list" | sed -n "${pick}p")
				NEW=0
				break
			fi
			[ "$pick" = $((i + 1)) ] && break
			warn "$(L "type a number from 1 to $((i + 1))." "введите число от 1 до $((i + 1)).")"
		done
	fi
fi

# ---- 3. mode ------------------------------------------------------------------
# An interface keeps its mode: between RAW and WireGuard what sits beside it
# changes too, its LAN rule or the WireGuard interface that rides on it.
if [ "$NEW" = 0 ]; then
	current=$(mode_of "$IFACE")
	[ -z "$MODE" ] || [ "$MODE" = "$current" ] ||
		die "$(L "$IFACE is in $(label "$current") mode: change the mode in LuCI, or add a new interface." "$IFACE в режиме $(label "$current"): режим меняется в LuCI, или добавьте новый интерфейс.")"
	MODE=$current
elif [ -z "$MODE" ]; then
	say ""
	say "$(L 'Mode - the link does not say which; the server decides what it accepts:' 'Режим - в ссылке его нет; какие режимы принимает сервер, решает сервер:')"
	say "  1) RAW                    $(L "the tunnel carries the LAN's traffic itself (default)" 'туннель сам везет трафик LAN (по умолчанию)')"
	say "  2) WireGuard              $(L 'relays a WireGuard interface to the server, over DTLS' 'ретранслирует интерфейс WireGuard до сервера, через DTLS')"
	say "  3) $(L 'WireGuard without DTLS the same, to a server with a -listen-direct listener' 'WireGuard без DTLS     то же, для сервера со слушателем -listen-direct')"
	while :; do
		choice=$(ask "$(L 'Mode' 'Режим') [1]:")
		case "$choice" in
		''|1) MODE=raw; break ;;
		2) MODE=wg; break ;;
		3) MODE=wg-direct; break ;;
		*) warn "$(L 'type 1, 2 or 3.' 'введите 1, 2 или 3.')" ;;
		esac
	done
fi

# The WireGuard interface that carries a WireGuard-mode tunnel's traffic.
c=${IFACE}_wg
if [ "$MODE" != raw ]; then
	[ ${#c} -le 15 ] ||
		die "$(L "$c, the name of its WireGuard interface, is longer than the 15 characters a device name can have." "$c, имя его интерфейса WireGuard, длиннее 15 символов, допустимых для имени устройства.")"
	[ -z "$(uci -q get "network.$c")" ] || [ "$(uci -q get "network.$c.proto")" = wireguard ] ||
		die "$(L "network.$c is already taken by an interface that is not WireGuard." "network.$c уже занят интерфейсом, который не WireGuard.")"
fi

# ---- 4. device ID ---------------------------------------------------------------
# Not invented here: the server ties a password to the device IDs that use it,
# so the operator chooses one, unique per tunnel.
used_by() { # used_by ID -> the other qWDTT interface with this device ID
	for t in $(tunnels); do
		[ "$t" != "$IFACE" ] || continue
		[ "$(uci -q get "network.$t.device_id")" != "$1" ] || { echo "$t"; return; }
	done
}

if [ -n "$DEVICE_ID" ]; then
	other=$(used_by "$DEVICE_ID")
	[ -z "$other" ] ||
		die "$(L "device ID $DEVICE_ID is already used by $other." "идентификатор $DEVICE_ID уже занят интерфейсом $other.")"
else
	current=$(uci -q get "network.$IFACE.device_id" || true)
	say ""
	say "$(L 'Device ID - how the server tells this tunnel apart. Unique per tunnel:' 'Идентификатор устройства - по нему сервер различает туннели. Свой у каждого:')"
	say "$(L 'two tunnels with one ID are disconnected by the server in turn.' 'два туннеля с одним идентификатором сервер отключает по очереди.')"
	while :; do
		if [ -n "$current" ]; then
			DEVICE_ID=$(ask "$(L 'Device ID' 'Идентификатор') [$current]:")
			[ -n "$DEVICE_ID" ] || DEVICE_ID=$current
		else
			DEVICE_ID=$(ask "$(L 'Device ID (e.g. home-router):' 'Идентификатор (например, home-router):')")
			[ -n "$DEVICE_ID" ] || { warn "$(L 'a device ID is needed.' 'нужен идентификатор устройства.')"; continue; }
		fi
		other=$(used_by "$DEVICE_ID")
		[ -z "$other" ] && break
		warn "$(L "$other already uses $DEVICE_ID." "$DEVICE_ID уже занят интерфейсом $other.")"
	done
fi

# ---- 5. what the link sets, and confirmation -------------------------------------
json=$(parse_link "$MODE") || { unreadable "$json"; exit 1; }
LINK=

# Secrets reach jsonfilter and uci on stdin: as an argument they would show in
# the process list for as long as either runs.
get() { printf '%s' "$json" | jsonfilter -i /dev/stdin -e "@.$1" 2>/dev/null || true; }
uci_secret() { # uci_secret OPTION VALUE
	printf "set %s='%s'\n" "$1" "$(printf '%s' "$2" | sed "s/'/'\\\\''/g")" | uci batch
}
host=$(get peer_host)
port=$(get peer_port)
lport=$(get listen_port)
pass=$(get password)
hashes=$(get 'hashes[*]')
[ -n "$hashes" ] || [ "$NEW" = 0 ] ||
	die "$(L 'the link carries no VK call hash, and a new tunnel needs one.' 'в ссылке нет хеша звонка VK, а новому туннелю он нужен.')"

existing=" $(uci -q get "network.$IFACE.hash" || true) "
added=0
for h in $hashes; do
	case "$existing" in *" $h "*) ;; *) added=$((added + 1)) ;; esac
done
total=$(( $(echo "$existing" | wc -w) + added ))
defport=$([ "$MODE" = raw ] && echo 56003 || echo 56000)

say ""
say "$(L 'About to apply:' 'Будет применено:')"
say "  $(L 'interface ' 'интерфейс ')  $IFACE $([ "$NEW" = 1 ] && L '(new)' '(новый)' || L '(reconfigured)' '(перенастройка)')"
say "  $(L 'mode      ' 'режим     ')  $(label "$MODE")"
say "  $(L 'server    ' 'сервер    ')  $host, $(L port порт) ${port:-$(L "default ($defport)" "по умолчанию ($defport)")}"
say "  $(L 'device ID ' 'ID        ')  $DEVICE_ID"
say "  $(L 'hashes    ' 'хеши      ')  $(L "$added new from the link, $total in all" "из ссылки новых: $added, всего: $total")"
for w in $(get 'warnings[*]'); do
	[ "$w" != admin_port ] ||
		warn "$(L "the link names port 56002, which the app's admin screen puts there for its admin API: the default port is used instead." 'в ссылке порт 56002: так админ-экран приложения указывает порт админ-API. Используется порт по умолчанию.')"
done
if [ "$CONFIRM" = 1 ]; then
	choice=$(ask "$(L 'Apply? [Y/n]:' 'Применить? [Y/n]:')")
	case "$choice" in
	''|y|Y|yes|Yes|д|Д|да|Да) ;;
	*) msg "$(L 'nothing changed.' 'ничего не изменено.')"; exit 0 ;;
	esac
fi

# ---- 6. apply -------------------------------------------------------------------

# The lowest table no interface uses, from where the client's own default sat.
free_table() {
	used=" $(uci show network | sed -n "s/^network\.[^.]*\.ip4table='\([0-9]*\)'$/\1/p" | tr '\n' ' ') "
	t=51820
	while case "$used" in *" $t "*) true ;; *) false ;; esac; do t=$((t + 1)); done
	echo $t
}

# Below every existing rule, as LuCI does, so a tunnel added later is asked
# first; never 10000, where netifd puts rules of its own.
free_priority() {
	low=$(uci show network | sed -n "s/^network\.[^.]*\.priority='\([0-9]*\)'$/\1/p" | sort -n | head -n 1)
	p=$(( ${low:-10000} - 1 ))
	[ "$p" != 10000 ] || p=9999
	echo $p
}

add_rule() { # add_rule NAME TABLE
	uci set "network.$1=rule"
	uci set "network.$1.in=lan"
	uci set "network.$1.lookup=$2"
	uci set "network.$1.priority=$(free_priority)"
}

port_owner() { # port_owner PORT -> the qWDTT interface other than IFACE using it
	for t in $(tunnels); do
		[ "$t" != "$IFACE" ] || continue
		[ "$(uci -q get "network.$t.mode")" = wireguard ] || continue
		[ "$(uci -q get "network.$t.listen_port" || echo 9000)" != "$1" ] || { echo "$t"; return; }
	done
}

s=network.$IFACE
uci set "$s=interface"
uci set "$s.proto=qwdtt"
uci set "$s.peer_host=$host"
if [ -n "$port" ]; then uci set "$s.peer_port=$port"; else uci -q delete "$s.peer_port" || true; fi
uci_secret "$s.password" "$pass"
pass=
for h in $hashes; do
	case "$existing" in *" $h "*) continue ;; esac
	uci add_list "$s.hash=$h"
done
uci set "$s.device_id=$DEVICE_ID"

case "$MODE" in
raw)
	if [ "$NEW" = 1 ]; then
		table=$(free_table)
		uci set "$s.ip4table=$table"
		add_rule "${IFACE}_rule" "$table"
	fi
	;;
wg|wg-direct)
	uci set "$s.mode=wireguard"
	if [ "$MODE" = wg-direct ]; then uci set "$s.no_dtls=1"; else uci -q delete "$s.no_dtls" || true; fi
	# The local endpoint stays where it is on an existing tunnel: the
	# WireGuard interface already points at it.
	if [ "$NEW" = 1 ]; then
		lp=${lport:-9000}
		while [ -n "$(port_owner "$lp")" ]; do lp=$((lp + 1)); done
		uci set "$s.listen_port=$lp"
	fi
	;;
esac

uci commit network
msg "$(L "$IFACE written." "$IFACE записан.")"

start=$([ "$NEW" = 1 ] && echo /etc/init.d/network reload || echo ifup "$IFACE")
if [ "$BRING_UP" = 0 ]; then
	msg "$(L "not started (-n). Start it with: $start" "не запущен (-n). Запуск: $start")"
	[ "$MODE" = raw ] ||
		msg "$(L "then run this again with -i $IFACE to set up its WireGuard interface." "затем запустите этот скрипт снова с -i $IFACE, чтобы настроить его интерфейс WireGuard.")"
	exit 0
fi

# Started once: ifup reloads before it restarts, and the reload alone starts a
# new or changed interface. Taken down first, an existing one waits for the up.
# The restart clears its old WireGuard config, so the one awaited is this server's.
if [ "$NEW" = 1 ]; then
	/etc/init.d/network reload
else
	ifdown "$IFACE"
	rm -f "$RUN_DIR/$IFACE.wg"
	ifup "$IFACE"
fi

if [ "$MODE" = raw ]; then
	msg "$(L "started. Watch it with: logread -f -e $IFACE" "запущен. Следить за ним: logread -f -e $IFACE")"
	exit 0
fi

# ---- WireGuard: the interface that carries the traffic ----------------------
# The relay only forwards; the server issues the WireGuard config once the
# relay has connected, and the client writes it to $RUN_DIR/$IFACE.wg.
msg "$(L "waiting up to ${WAIT}s for the server's WireGuard config ..." "ожидание конфигурации WireGuard от сервера, до ${WAIT} с ...")"
i=0
while [ ! -s "$RUN_DIR/$IFACE.wg" ]; do
	err=$(ifstatus "$IFACE" | jsonfilter -e '@.errors[0].code' 2>/dev/null || true)
	[ -z "$err" ] ||
		die "$(L "$IFACE reports $err: see Network -> Interfaces or logread -e $IFACE, then run this again with -i $IFACE." "$IFACE сообщает $err: см. Сеть -> Интерфейсы или logread -e $IFACE, затем запустите скрипт снова с -i $IFACE.")"
	[ $i -lt "$WAIT" ] ||
		die "$(L "no WireGuard config after ${WAIT}s: check logread -e $IFACE, then run this again with -i $IFACE." "за ${WAIT} с конфигурация WireGuard не пришла: проверьте logread -e $IFACE и запустите скрипт снова с -i $IFACE.")"
	sleep 5
	i=$((i + 5))
done

conf=$RUN_DIR/$IFACE.wg
key=$(sed -n 's/^PrivateKey *= *//p' "$conf")
addrs=$(sed -n 's/^Address *= *//p' "$conf" | tr ',' ' ')
pub=$(sed -n 's/^PublicKey *= *//p' "$conf")
psk=$(sed -n 's/^PresharedKey *= *//p' "$conf")
[ -n "$key" ] && [ -n "$addrs" ] && [ -n "$pub" ] ||
	die "$(L "$conf is not a WireGuard config this can read." "$conf - не конфигурация WireGuard, которую удалось бы прочитать.")"

lp=$(uci -q get "$s.listen_port" || echo 9000)
if [ -z "$(uci -q get "network.$c")" ]; then
	other=$(uci show network | sed -n "s/^network\.\([^.]*\)\.endpoint_port='$lp'$/\1/p" | head -n 1)
	[ -z "$other" ] ||
		die "$(L "network.$other already points a WireGuard peer at port $lp: update its keys from $conf by hand." "network.$other уже направляет узел WireGuard на порт $lp: обновите его ключи из $conf вручную.")"
	carrier_new=1
else
	carrier_new=0
fi

uci set "network.$c=interface"
uci set "network.$c.proto=wireguard"
uci_secret "network.$c.private_key" "$key"
uci -q delete "network.$c.addresses" || true
for a in $addrs; do uci add_list "network.$c.addresses=$a"; done

p=${c}_peer
uci set "network.$p=wireguard_$c"
uci set "network.$p.public_key=$pub"
if [ -n "$psk" ]; then uci_secret "network.$p.preshared_key" "$psk"; else uci -q delete "network.$p.preshared_key" || true; fi
uci set "network.$p.endpoint_host=127.0.0.1"
uci set "network.$p.endpoint_port=$lp"

if [ "$carrier_new" = 1 ]; then
	table=$(free_table)
	uci set "network.$c.mtu=1280"
	uci set "network.$c.ip4table=$table"
	uci set "network.$p.persistent_keepalive=25"
	uci add_list "network.$p.allowed_ips=0.0.0.0/0"
	uci set "network.$p.route_allowed_ips=1"
	add_rule "${c}_rule" "$table"

	uci set "firewall.${c}_nat=nat"
	uci set "firewall.${c}_nat.name=$c-masq"
	uci set "firewall.${c}_nat.family=ipv4"
	uci set "firewall.${c}_nat.src=qwdtt"
	uci set "firewall.${c}_nat.device=$c"
	uci set "firewall.${c}_nat.target=MASQUERADE"
fi
# Off the zone's qwdtt+ pattern, it joins by name, as qwdtt-up.sh has a relay do.
case "$c" in
qwdtt*) ;;
*) uci -q get firewall.qwdtt.network | tr ' ' '\n' | grep -qx "$c" ||
	uci add_list "firewall.qwdtt.network=$c" ;;
esac
uci commit firewall
/etc/init.d/firewall reload >/dev/null 2>&1 || true
uci commit network
/etc/init.d/network reload
ifup "$c"
if [ "$carrier_new" = 1 ]; then
	msg "$(L "$c: WireGuard interface added, routing the LAN through $IFACE." "$c: интерфейс WireGuard добавлен, трафик LAN идет через $IFACE.")"
else
	msg "$(L "$c: WireGuard interface updated, routing the LAN through $IFACE." "$c: интерфейс WireGuard обновлен, трафик LAN идет через $IFACE.")"
fi
msg "$(L "watch it with: logread -f -e $IFACE" "следить за ним: logread -f -e $IFACE")"
