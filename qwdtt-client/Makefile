# SPDX-License-Identifier: GPL-3.0-or-later
#
# The qWDTT client, packaged from a binary that CI cross-compiles before the
# SDK runs.
#
# Building it from source inside the SDK needs go >= 1.25, which the 24.10 feed
# cannot supply, and that floor is what kept this package off 24.10 and made
# the qwdtt package unable to depend on it. The client is a static
# CGO_ENABLED=0 ELF, so the target toolchain contributes nothing to it:
# packaging the prebuilt binary instead builds on every SDK in seconds.

include $(TOPDIR)/rules.mk

PKG_NAME:=qwdtt-client
# The repository's tag, shared with qwdtt and luci-app-qwdtt.
PKG_VERSION:=1.0.2
PKG_RELEASE:=1

PKG_MAINTAINER:=
PKG_LICENSE:=GPL-3.0-or-later

include $(INCLUDE_DIR)/package.mk

define Package/qwdtt-client
  SECTION:=net
  CATEGORY:=Network
  SUBMENU:=VPN
  TITLE:=qWDTT tunnel client
  URL:=https://github.com/code2fingers/qwdtt-openwrt
  DEPENDS:=+ca-bundle +kmod-tun +ip-full
endef

define Package/qwdtt-client/description
The client half of qWDTT, a novel VPN based on the WDTT protocol family that
tunnels traffic through VK TURN servers. It creates the TUN interface and
carries the LAN's IPv4 traffic to a qWDTT server.

Neither the control script nor the LuCI page is here: see the qwdtt and
luci-app-qwdtt packages.
endef

# Nothing is fetched or compiled; package.mk still wants the directory.
define Build/Prepare
	mkdir -p $(PKG_BUILD_DIR)
endef

define Build/Configure
endef

define Build/Compile
endef

# The test comes first so a missing binary fails the build here rather than
# shipping a package with an empty /usr/bin/qwdtt-client: it means whoever
# invoked the SDK skipped the cross-compile that stages it.
define Package/qwdtt-client/install
	test -f ./files/qwdtt-client
	$(INSTALL_DIR) $(1)/usr/bin
	$(INSTALL_BIN) ./files/qwdtt-client $(1)/usr/bin/qwdtt-client
endef

$(eval $(call BuildPackage,qwdtt-client))
