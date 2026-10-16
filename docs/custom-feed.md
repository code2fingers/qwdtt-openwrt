# Собственный feed пакетов (GitHub Pages)

Репозиторий публикует подписанный feed пакетов qWDTT на GitHub Pages. В отличие
от разовой загрузки файлов из Releases, feed дает роутеру доверенный источник:
индекс подписан ключом проекта, а `apk` и `opkg` проверяют подпись сами.

Поддерживаются обе актуальные ветки OpenWrt, каждая в своем формате:

| OpenWrt | Менеджер пакетов | Формат | Индекс | Собрано против |
| --- | --- | --- | --- | --- |
| 25.12 | `apk` | `.apk` | `packages.adb` | 25.12.5 |
| 24.10 | `opkg` | `.ipk` | `Packages` + `Packages.sig` | 24.10.8 |

Корень feed:

`https://code2fingers.github.io/qwdtt-openwrt/`

Feed публикуется workflow `.github/workflows/release.yml` при выпуске версии.

## Структура feed

Пакеты лежат по архитектуре пакетов (`pkgarch`), а не по устройству:

`/releases/<openwrt-release>/<pkgarch>/`

Пример:

`/releases/25.12/aarch64_cortex-a53/`

Одна архитектура обслуживает много целей, поэтому на сайте есть отдельное
дерево навигации, построенное так, как о роутере думает пользователь. Пока оно
охватывает самые распространенные цели: 38 для 25.12 и 31 для 24.10. Для
остальных архитектур feed тоже собран и лежит по той же схеме
`/releases/<openwrt-release>/<pkgarch>/`, а инструкции ниже подходят без
изменений:

`/releases/<openwrt-release>/`

`/releases/<openwrt-release>/<target>/`

`/releases/<openwrt-release>/<target>/<subtarget>/`

Пример: `/releases/25.12/mediatek/filogic/`

Страница `<target>/<subtarget>/` - это то, что нужно открыть. Она не содержит
пакетов, а показывает готовые команды для вашего роутера, уже подставив нужную
архитектуру.

В feed публикуются:

- `.apk` или `.ipk` пакеты: `qwdtt-client` (под архитектуру),
  `luci-proto-qwdtt`, `luci-i18n-qwdtt-ru` (не зависят от архитектуры)
- индекс: `packages.adb` для 25.12, `Packages`, `Packages.gz` и `Packages.sig`
  для 24.10
- публичные ключи для проверки подписи индекса

Каждый каталог архитектуры самодостаточен: в нем лежит и клиент под эту
архитектуру, и два общих пакета. Один URL дает роутеру все, что нужно.

## Как узнать свою архитектуру

Если не знаете `target/subtarget` своего роутера, спросите сам роутер:

```sh
. /etc/openwrt_release && echo "$DISTRIB_TARGET"
```

Для архитектуры пакетов:

```sh
. /etc/openwrt_release && echo "$DISTRIB_ARCH"
```

Либо просто найдите свою модель на
[странице feed](https://code2fingers.github.io/qwdtt-openwrt/) и перейдите по
дереву до нужного subtarget - там уже будут готовые команды.

## OpenWrt 25.12 (apk)

Отдельные `.apk` не подписаны ключом проекта: OpenWrt подписывает индекс, а не
файлы пакетов. Доверие дает установленный публичный ключ, поэтому ставить его
нужно до добавления feed.

Замените `ARCH` на свою архитектуру.

```sh
mkdir -p /etc/apk/keys /etc/apk/repositories.d
wget -O /etc/apk/keys/qwdtt.pem \
  https://code2fingers.github.io/qwdtt-openwrt/qwdtt.pem
echo "https://code2fingers.github.io/qwdtt-openwrt/releases/25.12/ARCH/packages.adb" \
  > /etc/apk/repositories.d/qwdtt.list
apk update
```

Установка:

```sh
apk add qwdtt-client luci-proto-qwdtt
```

Русификация интерфейса LuCI - отдельным пакетом:

```sh
apk add luci-i18n-qwdtt-ru
```

## OpenWrt 24.10 (opkg)

То же самое, но и feed, и ключ - свои: `opkg` читает текстовый индекс
`Packages` и проверяет `Packages.sig` через `usign`. Это другой ключ и другой
инструмент, чем на стороне `apk`.

Ключ должен лежать в `/etc/opkg/keys` под именем своего key id - `opkg` ищет
его именно так. Для этого feed key id постоянный: `fa06b754936d35c5`.

Замените `ARCH` на свою архитектуру.

```sh
mkdir -p /etc/opkg/keys
wget -O /etc/opkg/keys/fa06b754936d35c5 \
  https://code2fingers.github.io/qwdtt-openwrt/qwdtt-usign.pub
echo "src/gz qwdtt https://code2fingers.github.io/qwdtt-openwrt/releases/24.10/ARCH" \
  > /etc/opkg/qwdtt.conf
opkg update
```

Установка:

```sh
opkg install qwdtt-client luci-proto-qwdtt
```

Русификация интерфейса LuCI - отдельным пакетом:

```sh
opkg install luci-i18n-qwdtt-ru
```

## Без SSH, через LuCI

Ключ нельзя положить в `/etc/apk/keys` из веб-интерфейса: модалка Configuration
в менеджере пакетов правит только список репозиториев. Поэтому ключ приходит
архивом резервной копии.

1. Скачайте со страницы [Releases](../../../releases/latest) архив
   `qwdtt-apk-key.tar.gz` для 25.12 или `qwdtt-opkg-key.tar.gz` для 24.10.
2. Восстановите его в System -> Backup / Flash Firmware -> "Restore". Архив
   кладет публичный ключ в `/etc/apk/keys` или в `/etc/opkg/keys`
   соответственно.
3. В System -> Software -> Configuration допишите строку feed для своей
   архитектуры - ту же, что в разделах выше.
4. Сохраните, нажмите "Update lists…", затем поставьте `qwdtt-client` и
   `luci-proto-qwdtt` в System -> Software.

Загружать `.apk` файлом через LuCI бессмысленно: бэкенд вызывает
`apk add <файл>` без `--allow-untrusted`, а отдельные пакеты ключом проекта не
подписаны.

## Публичные ключи

Ключи лежат по постоянным путям и не меняются от релиза к релизу:

| Формат | Файл | Назначение |
| --- | --- | --- |
| apk | [`qwdtt.pem`](https://code2fingers.github.io/qwdtt-openwrt/qwdtt.pem) | проверка `packages.adb` |
| opkg | [`qwdtt-usign.pub`](https://code2fingers.github.io/qwdtt-openwrt/qwdtt-usign.pub) | проверка `Packages.sig` |

Те же файлы лежат в репозитории в каталоге [`keys/`](../keys), так что их можно
сверить с тем, что отдает Pages.

Приватные ключи хранятся в GitHub Secrets и в репозиторий не попадают.
Подписывается индекс, а не отдельные пакеты: `apk` проверяет подпись индекса, а
целостность самих пакетов - по хешам, записанным в этом индексе. Для `opkg`
схема та же, только индекс текстовый, а подпись отдельным файлом.

Каждая публикация feed проверяет свою же подпись только публичным ключом, в том
же задании, которое ее поставило. Если эта проверка прошла, роутер с
установленным ключом ставит пакеты без `--allow-untrusted`.

## Проверка

Минимальная проверка, что feed виден и доверен:

```sh
apk update && apk add --simulate luci-proto-qwdtt
```

```sh
opkg update && opkg install --noaction luci-proto-qwdtt
```

Что смотреть, если не работает:

- **`UNTRUSTED signature` или `Signature check failed`** - публичный ключ не
  установлен или лежит не там. Для `opkg` проверьте, что имя файла в
  `/etc/opkg/keys` - это `fa06b754936d35c5`, а не что-то другое.
- **`qwdtt-client` не найден, и не ставится ни один пакет** - в URL feed
  указана чужая архитектура. `luci-proto-qwdtt` зависит от `qwdtt-client`, а
  перевод - от `luci-proto-qwdtt`, поэтому без клиента не ставятся и они.
  Каталог в URL должен совпадать с `DISTRIB_ARCH` роутера.
- **404 при `update`** - ветка OpenWrt и путь в URL не совпадают. Роутеру на
  24.10 нужен `/releases/24.10/`, на 25.12 - `/releases/25.12/`. Форматы не
  взаимозаменяемы.
- **`opkg` не проверяет подпись** - в системе нет `usign`. Без него подписанный
  feed проверить нечем: `opkg install usign`.
- **Архитектуры нет в списке** - сборка под нее не выпускается. Пакеты из
  Releases в этом случае тоже не помогут, они собраны для тех же архитектур.

## Установка одной командой

Все перечисленное умеет делать установщик: он сам определяет менеджер пакетов,
архитектуру и ветку OpenWrt, ставит ключ, добавляет feed и пакеты.

```sh
sh <(wget -O - https://raw.githubusercontent.com/code2fingers/qwdtt-openwrt/main/install.sh)
```

Настройка клиента после установки описана в [README](../README.md).
