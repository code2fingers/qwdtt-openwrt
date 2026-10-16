# qWDTT for OpenWrt

[![Release](https://img.shields.io/github/v/release/code2fingers/qwdtt-openwrt)](https://github.com/code2fingers/qwdtt-openwrt/releases/latest)
[![Release date](https://img.shields.io/github/release-date/code2fingers/qwdtt-openwrt)](https://github.com/code2fingers/qwdtt-openwrt/releases/latest)
[![Downloads](https://img.shields.io/github/downloads/code2fingers/qwdtt-openwrt/total)](https://github.com/code2fingers/qwdtt-openwrt/releases)


qWDTT-клиент для роутеров OpenWrt с возможностью настройки через
UCI и веб-интерфейс LuCI, а также с отдельной страницей LuCI для
отслеживания состояния туннелей qWDTT.
Клиент поддерживает два режима работы - **RAW** и **WireGuard**.

Репозиторий также содержит обновляемый
[feed пакетов OpenWrt](https://code2fingers.github.io/qwdtt-openwrt/)
для удобной установки и обновления.

[SpaceNeuroX/qwdtt-openwrt](https://github.com/SpaceNeuroX/qwdtt-openwrt) -
исходный проект клиента qWDTT для OpenWrt, на основе которого создан этот
проект.

## Требования

- Роутер с OpenWrt 24.10 или 25.12.
- Свободное место на флеш-памяти: 7-9 МБ в зависимости от типа памяти
  роутера, на x86 - около 15 МБ.
- Настроенный сервер qWDTT с включенным слушателем нужного режима: **RAW**
  (обычно UDP-порт 56003) или основным для **WireGuard** (обычно 56000).
- Данные подключения: адрес сервера, пароль и хеш звонка VK.

## Быстрый старт

Выполнить на роутере по SSH:

```sh
sh <(wget -O - https://raw.githubusercontent.com/code2fingers/qwdtt-openwrt/main/install.sh)
```

Альтернативный вариант без установки пакета с русским переводом `luci-i18n-qwdtt-ru`:

```sh
sh <(wget -O - https://raw.githubusercontent.com/code2fingers/qwdtt-openwrt/main/install.sh) -en
```

Скрипт определяет менеджер пакетов, добавляет feed и его ключ, устанавливает пакеты
`qwdtt-client`, `luci-proto-qwdtt` и пакет с русским переводом `luci-i18n-qwdtt-ru`.

Дальше - раздел [Настройка](#настройка).

## Возможности

- Два режима: **RAW** - интерфейс сам является туннелем и получает адрес от
  сервера; **WireGuard** - интерфейс только ретранслирует локальный UDP-порт
  до WireGuard на сервере.
- Поддержка нескольких туннелей одновременно, в том числе к разным серверам.
- Настройка через LuCI на странице Network -> Interfaces.
- Страница состояния с возможностью проверки туннелей.

## Установка и обновление

Пакеты раздаются через подписанный
[feed](https://code2fingers.github.io/qwdtt-openwrt/): роутер проверяет
подпись индекса при обновлении списков пакетов. Подробно о feed, архитектурах
и ключах - [docs/custom-feed.md](docs/custom-feed.md). Сами пакеты лежат
также на странице [Releases](../../releases/latest).

Способы установки:

1. **Одной командой** по SSH - см. [Быстрый старт](#быстрый-старт).
2. **Вручную через менеджер пакетов**: добавить feed и его ключ, затем
   `opkg install` или `apk add` - см. [docs/custom-feed.md](docs/custom-feed.md).
3. **Через LuCI, без SSH**:
   1. В System -> Administration -> Repo Public Keys вставить адрес ключа и
      нажать Add key:
      - 25.12: `https://code2fingers.github.io/qwdtt-openwrt/qwdtt.pem`
      - 24.10: `https://code2fingers.github.io/qwdtt-openwrt/fa06b754936d35c5`
   2. В System -> Software -> Configuration добавить строку feed для своей
      архитектуры (адреса - на [странице feed](https://code2fingers.github.io/qwdtt-openwrt/))
      и нажать Update lists.
   3. Установить `qwdtt-client`, `luci-proto-qwdtt` и `luci-i18n-qwdtt-ru`.

**Обновление.** Новые версии приходят через тот же feed: кнопкой Upgrade в
System -> Software, повторным запуском `install.sh` или командой

```sh
opkg update && opkg upgrade qwdtt-client luci-proto-qwdtt luci-i18n-qwdtt-ru   # 24.10
apk update && apk upgrade qwdtt-client luci-proto-qwdtt luci-i18n-qwdtt-ru     # 25.12
```

Настройки туннелей хранятся в стандартном файле `/etc/config/network` и при обновлении
сохраняются. Туннели после обновления перезапускаются автоматически.

**Удаление.**

```sh
opkg remove luci-i18n-qwdtt-ru luci-proto-qwdtt qwdtt-client   # 24.10
apk del luci-i18n-qwdtt-ru luci-proto-qwdtt qwdtt-client       # 25.12
```

Интерфейсы туннелей в `/etc/config/network` при этом остаются. Если туннель работал
в режиме WireGuard, перед удалением удалить интерфейс `qwdtt0_wg` и его правило
`qwdtt0_wg_rule`.

## Настройка

Предполагается, что сервер qWDTT на VPS уже настроен.

Для подключения нужны несколько параметров. Их также можно взять
из ссылки `wdtt://` или `qwdtt://`:

| Параметр | В ссылке `wdtt://` | В ссылке `qwdtt://` |
| --- | --- | --- |
| Адрес сервера | `add` | хост в `peer` |
| Порт сервера | `dtls` (для WireGuard) | порт в `peer` |
| Пароль | `id` | `pass` |
| Хеш звонка VK | - | `hashes` |
| Идентификатор устройства | `did`, если есть | - |
| Локальный порт (WireGuard) | `lp` | `port` |

Дальнейшая настройка зависит от выбранного режима: **RAW** или **WireGuard**.

<details>
<summary><b>RAW</b></summary>

**Через LuCI**

1. Network -> Interfaces -> Add new interface, протокол qWDTT, имя `qwdtt0`.
2. На вкладке qWDTT выбрать режим RAW и заполнить адрес сервера,
   идентификатор устройства, пароль и хеши. Хеши добавляются по одному
   кнопкой «+»; можно вставить и ссылку на звонок.
3. Галочка «Маршрутизировать трафик LAN-клиентов в этот туннель» уже включена,
   свободная таблица маршрутизации подставлена сама.
4. Save & Apply.

**Через UCI**

```sh
uci set network.qwdtt0=interface
uci set network.qwdtt0.proto='qwdtt'
uci set network.qwdtt0.ip4table='51820'
uci set network.qwdtt0.peer_host='IP_АДРЕС_СЕРВЕРА'
uci set network.qwdtt0.device_id='ИДЕНТИФИКАТОР_УСТРОЙСТВА'
uci set network.qwdtt0.password='ПАРОЛЬ'
uci add_list network.qwdtt0.hash='ХЕШ_ЗВОНКА'

uci set network.qwdtt0_rule=rule
uci set network.qwdtt0_rule.in='lan'
uci set network.qwdtt0_rule.lookup='51820'
uci set network.qwdtt0_rule.priority='9999'

uci commit network
ifup qwdtt0
```

Своя таблица (`ip4table`) обязательна: маршрут по умолчанию через туннель в
основной таблице увел бы в него и трафик самого клиента к VK.

</details>

<details>
<summary><b>WireGuard</b></summary>

В этом режиме интерфейс qWDTT ничего не везет сам: он ретранслирует локальный
порт до WireGuard на сервере. Трафик несет обычный интерфейс WireGuard,
направленный на `127.0.0.1` и этот порт.

**Через LuCI**

1. Создать интерфейс qWDTT, как для RAW, но выбрать режим WireGuard. Порт
   сервера по умолчанию 56000, «Порт локальной точки подключения» - 9000, у
   каждого туннеля свой. Save & Apply.
2. Скопировать содержимое `/var/run/qwdtt/qwdtt0.wg` (для этого нужен SSH).
3. Network -> Interfaces -> Add new interface, протокол WireGuard, имя
   `qwdtt0_wg` - имя туннеля с `_wg`: так интерфейс окажется в списке сразу
   под туннелем и сам попадет в зону межсетевого экрана `qwdtt`. На вкладке
   General Settings нажать Import configuration -> Load configuration и
   вставить скопированное: ключи, адрес и узел заполнятся сами.
4. На вкладке Advanced Settings задать Override IPv4 routing table,
   например `51821`. У узла включить Route Allowed IPs.
5. В Network -> Firewall -> NAT Rules добавить правило: Outbound zone
   `qwdtt`, Outbound device `qwdtt0_wg`, Action MASQUERADE.
6. В Network -> Routing -> IPv4 Rules добавить правило: Incoming interface
   `lan`, Table `51821`. Save & Apply.

**Через UCI**

```sh
uci set network.qwdtt0=interface
uci set network.qwdtt0.proto='qwdtt'
uci set network.qwdtt0.mode='wireguard'
uci set network.qwdtt0.listen_port='9000'
uci set network.qwdtt0.peer_host='IP_АДРЕС_СЕРВЕРА'
uci set network.qwdtt0.device_id='ИДЕНТИФИКАТОР_УСТРОЙСТВА'
uci set network.qwdtt0.password='ПАРОЛЬ'
uci add_list network.qwdtt0.hash='ХЕШ_ЗВОНКА'
uci commit network
ifup qwdtt0
```

Когда появится `/var/run/qwdtt/qwdtt0.wg`, перенести значения из него в
интерфейс WireGuard:

```sh
uci set network.qwdtt0_wg=interface
uci set network.qwdtt0_wg.proto='wireguard'
uci set network.qwdtt0_wg.private_key='PrivateKey_ИЗ_ФАЙЛА'
uci add_list network.qwdtt0_wg.addresses='Address_ИЗ_ФАЙЛА'
uci set network.qwdtt0_wg.mtu='1280'
uci set network.qwdtt0_wg.ip4table='51821'

uci set network.qwdtt0_wg_peer=wireguard_qwdtt0_wg
uci set network.qwdtt0_wg_peer.public_key='PublicKey_ИЗ_ФАЙЛА'
uci set network.qwdtt0_wg_peer.endpoint_host='127.0.0.1'
uci set network.qwdtt0_wg_peer.endpoint_port='9000'
uci set network.qwdtt0_wg_peer.persistent_keepalive='25'
uci add_list network.qwdtt0_wg_peer.allowed_ips='0.0.0.0/0'
uci set network.qwdtt0_wg_peer.route_allowed_ips='1'

uci set network.qwdtt0_wg_rule=rule
uci set network.qwdtt0_wg_rule.in='lan'
uci set network.qwdtt0_wg_rule.lookup='51821'
uci set network.qwdtt0_wg_rule.priority='9998'

uci set firewall.qwdtt0_wg_nat=nat
uci set firewall.qwdtt0_wg_nat.name='qwdtt0_wg-masq'
uci set firewall.qwdtt0_wg_nat.family='ipv4'
uci set firewall.qwdtt0_wg_nat.src='qwdtt'
uci set firewall.qwdtt0_wg_nat.device='qwdtt0_wg'
uci set firewall.qwdtt0_wg_nat.target='MASQUERADE'

uci commit network
uci commit firewall
/etc/init.d/firewall reload
ifup qwdtt0_wg
```

</details>

## Проверка

- **Страница состояния** - Status -> qWDTT: сервер,
  идентификатор устройства, сколько сессий работает, адрес и счетчики.
  Блок «Проверка туннеля» пингует адрес через сам туннель, а туннель в режиме
  WireGuard - через его интерфейс WireGuard.
- **Ошибки** - если туннель не поднимается, причина отображается на странице
  интерфейса. После исправления нажать Restart на интерфейсе.
  Save & Apply сам туннель не перезапускает.
- **Логи** - в System -> System Log или по SSH. Рабочее подключение в режиме
  RAW пишет `RAW config received`, затем `TUN attached, traffic is flowing`.

```sh
logread -e qwdtt0
ip rule show
ip route show table 51820
```

## Лицензия

Этот проект распространяется под лицензией GNU General Public License v3.0.
