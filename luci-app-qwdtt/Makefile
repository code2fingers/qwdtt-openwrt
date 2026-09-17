# SPDX-License-Identifier: GPL-3.0-or-later
#
# luci.mk does the work: htdocs/ is installed under /www and root/ under /,
# so this file only has to declare the package. It is included by path through
# the luci feed, which is how a standalone repo added as a feed finds it --
# `include ../../luci.mk` only resolves inside the luci tree itself.

include $(TOPDIR)/rules.mk

PKG_NAME:=luci-app-qwdtt
# The repository's tag, shared with qwdtt and qwdtt-client; see qwdtt/Makefile.
PKG_VERSION:=1.1.0
PKG_RELEASE:=1

# Both are deliberately empty, and both are needed. PKG_MAINTAINER covers the
# generated luci-i18n-* packages, which fall through to package-defaults.mk.
# It does NOT reach this package: luci.mk overrides the field with
# MAINTAINER:=$(LUCI_MAINTAINER), whose default is "OpenWrt LuCI community" --
# so leaving LUCI_MAINTAINER unset would ship the app attributed to the LuCI
# project, which is worse than blank. Nothing validates either field.
PKG_MAINTAINER:=
LUCI_MAINTAINER:=
# Version 3 is required rather than chosen: overview.js translates two helpers
# out of the client's GPL-3.0 client/group.go, which is co-authored. The
# reasoning lives at the top of that file, next to the code it constrains.
#
# PKG_LICENSE_FILES stays unset even though LICENSE now exists: it names a
# path inside a package's unpacked sources, and this package has none --
# luci.mk installs htdocs/ and root/ straight from the feed.
PKG_LICENSE:=GPL-3.0-or-later

# Both, and deliberately the same string. "LuCI support for <thing>" is the
# phrasing the luci feed uses for titles, and luci.mk emits a description only
# when LUCI_DESCRIPTION is set -- there is no fallback to the title, so leaving
# it out would ship a package with no description at all.
LUCI_TITLE:=LuCI support for qWDTT
LUCI_DESCRIPTION:=LuCI support for qWDTT
# qwdtt is a sibling package in this feed providing /usr/bin/qwdtt, not
# shipped here: a headless router installs it alone, with no browser involved.
# Depending on it means installing this page still gets a working set of
# buttons.
LUCI_DEPENDS:=+luci-base +qwdtt
# Nothing here is compiled and nothing is architecture-specific: the package is
# JavaScript, JSON and POSIX shell, so one build serves every target.
LUCI_PKGARCH:=all

include $(TOPDIR)/feeds/luci/luci.mk

# call BuildPackage - OpenWrt buildroot signature
