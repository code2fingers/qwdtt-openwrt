# SPDX-License-Identifier: GPL-3.0-or-later
#
# luci.mk does the work: htdocs/ is installed under /www and root/ under /,
# so this file only has to declare the package. It is included by path through
# the luci feed, which is how a standalone repo added as a feed finds it --
# `include ../../luci.mk` only resolves inside the luci tree itself.

include $(TOPDIR)/rules.mk

PKG_NAME:=luci-proto-qwdtt
# The repository's tag, shared with qwdtt-client; see qwdtt-client/Makefile.
PKG_VERSION:=1.2.3
PKG_RELEASE:=1

# Both are deliberately empty, and both are needed. PKG_MAINTAINER covers the
# generated luci-i18n-* packages, which fall through to package-defaults.mk.
# It does NOT reach this package: luci.mk overrides the field with
# MAINTAINER:=$(LUCI_MAINTAINER), whose default is "OpenWrt LuCI community" --
# so leaving LUCI_MAINTAINER unset would ship the package attributed to the
# LuCI project, which is worse than blank. Nothing validates either field.
PKG_MAINTAINER:=
LUCI_MAINTAINER:=
# Version 3 is required rather than chosen: protocol/qwdtt.js translates two
# helpers out of the client's GPL-3.0 client/group.go, which is co-authored.
# The reasoning lives at the top of that file, next to the code it constrains.
PKG_LICENSE:=GPL-3.0-or-later

LUCI_TITLE:=Support for the qWDTT protocol
LUCI_DESCRIPTION:=Provides the qWDTT network protocol and its status page for LuCI
# qwdtt-client is a sibling package in this feed: it ships the client binary
# and the netifd protocol handler this page configures, so without it the
# protocol does not appear in the interface editor at all. rpcd-mod-ucode is
# what loads /usr/share/rpcd/ucode/luci.qwdtt, which the status page reads the
# tunnel's log through.
#
# luci-proto-wireguard is the other half of the tunnel's wireguard mode. The
# qWDTT tab configures the relay; the interface that carries the traffic is an
# ordinary WireGuard one, and without this the interface editor offers no way
# to create it - the protocol is simply absent from the list, on the one page
# the operator was sent to.
LUCI_DEPENDS:=+luci-base +qwdtt-client +rpcd-mod-ucode +luci-proto-wireguard
# Nothing here is compiled and nothing is architecture-specific: the package is
# JavaScript, JSON and ucode, so one build serves every target.
LUCI_PKGARCH:=all

include $(TOPDIR)/feeds/luci/luci.mk

# call BuildPackage - OpenWrt buildroot signature
