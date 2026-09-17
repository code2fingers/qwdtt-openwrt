# SPDX-License-Identifier: GPL-3.0-or-later
#
# A plain package rather than a luci.mk one, and separate from luci-app-qwdtt
# on purpose. This script is the tunnel's control layer and involves no
# browser, so a headless router needs it with no web interface installed.
# luci-app-qwdtt depends on it instead of shipping it.
#
# It ships the service layer: the control script, the procd init script that
# turns /etc/config/qwdtt into the client's command line, a default config and
# the firewall zone. Everything except the client binary, which is the separate
# qwdtt-client package.

include $(TOPDIR)/rules.mk

PKG_NAME:=qwdtt
# One tag versions the whole repository: the client, this script and the LuCI
# app all carry it, so the tag and the package version are always the same
# number and need no lookup table.
PKG_VERSION:=1.1.0
PKG_RELEASE:=1

# Deliberately empty, not forgotten. package-defaults.mk does
# MAINTAINER:=$(PKG_MAINTAINER), and nothing validates the field: it ends up
# as metadata only. Submitting to an official OpenWrt feed would require a
# real one.
PKG_MAINTAINER:=
PKG_LICENSE:=GPL-3.0-or-later

include $(INCLUDE_DIR)/package.mk

define Package/qwdtt
  SECTION:=net
  CATEGORY:=Network
  SUBMENU:=VPN
  TITLE:=Control script for the qWDTT tunnel client
  # No ping package: the probe binds the tunnel device with BusyBox ping's
  # -I <ifname>, which is enough to leave through the tunnel rather than the
  # default route. Confirmed by TTL, 117 through the tunnel against 108
  # without.
  #
  # The init script execs /usr/bin/qwdtt-client, so the service is dead
  # without it.
  DEPENDS:=+qwdtt-client
  # POSIX shell only: nothing is compiled, so one build serves every target.
  PKGARCH:=all
endef

define Package/qwdtt/description
Command-line control for qWDTT, a novel VPN based on the WDTT protocol family
that tunnels traffic through VK TURN servers.
endef

# No sources to fetch or build; package.mk still wants the directory.
define Build/Prepare
	mkdir -p $(PKG_BUILD_DIR)
endef

define Build/Configure
endef

define Build/Compile
endef

# /etc/config/qwdtt carries the peer, the password and the hashes, so an
# upgrade must never overwrite it.
define Package/qwdtt/conffiles
/etc/config/qwdtt
endef

define Package/qwdtt/install
	$(INSTALL_DIR) $(1)/usr/bin
	$(INSTALL_BIN) ./files/qwdtt $(1)/usr/bin/qwdtt
	$(INSTALL_DIR) $(1)/etc/init.d
	$(INSTALL_BIN) ./files/qwdtt.init $(1)/etc/init.d/qwdtt
	$(INSTALL_DIR) $(1)/etc/config
	$(INSTALL_CONF) ./files/qwdtt.config $(1)/etc/config/qwdtt
	# 0600, not the 0644 INSTALL_CONF gives: this file holds the connection
	# password once the user fills it in.
	chmod 0600 $(1)/etc/config/qwdtt
	$(INSTALL_DIR) $(1)/etc/uci-defaults
	$(INSTALL_BIN) ./files/qwdtt.defaults $(1)/etc/uci-defaults/99-qwdtt
endef

$(eval $(call BuildPackage,qwdtt))
