# SPDX-License-Identifier: GPL-3.0-or-later
#
# The qWDTT client, packaged from a binary that CI cross-compiles before the
# SDK runs.
#
# Building it from source inside the SDK needs go >= 1.25, which the 24.10 feed
# cannot supply, and that floor is what kept this package off 24.10 entirely.
# The client is a static CGO_ENABLED=0 ELF, so the target toolchain contributes
# nothing to it: packaging the prebuilt binary instead builds on every SDK in
# seconds.

include $(TOPDIR)/rules.mk

PKG_NAME:=qwdtt-client
# The repository's tag, shared with luci-proto-qwdtt.
PKG_VERSION:=1.1.0
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

It also ships the netifd protocol handler, so a tunnel is an interface with
option proto 'qwdtt' in /etc/config/network rather than a service of its own.
The web interface for it is the separate luci-proto-qwdtt package.
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
	$(INSTALL_DIR) $(1)/lib/netifd/proto
	$(INSTALL_BIN) ./files/qwdtt.sh $(1)/lib/netifd/proto/qwdtt.sh
	$(INSTALL_BIN) ./files/qwdtt-up.sh $(1)/lib/netifd/qwdtt-up.sh
	$(INSTALL_BIN) ./files/qwdtt-error.sh $(1)/lib/netifd/qwdtt-error.sh
	$(INSTALL_DIR) $(1)/etc/uci-defaults
	# Before the firewall one, so the zone's network list sees the interfaces
	# the conversion has just written.
	$(INSTALL_BIN) ./files/qwdtt.migrate $(1)/etc/uci-defaults/98-qwdtt-netifd
	$(INSTALL_BIN) ./files/qwdtt.defaults $(1)/etc/uci-defaults/99-qwdtt
endef

# Bring the tunnels down while there is still a protocol handler to do it with.
# Nothing else stops them: netifd started the client and goes on supervising it,
# so removing the files underneath leaves the process running on an inode that
# is already unlinked, carrying the LAN through a tunnel whose firewall zone
# postrm has just taken away. The device is deleted here rather than by the
# handler's teardown, which keeps it on purpose so that a restarting tunnel
# holds its interface index.
define Package/qwdtt-client/prerm
#!/bin/sh
[ -n "$${IPKG_INSTROOT}" ] && exit 0

for tunnel in $$(uci -q show network | \
	sed -n "s/^network\.\([^.=]*\)\.proto='qwdtt'$$/\1/p"); do
	ifdown "$$tunnel" 2>/dev/null
	ip link del "$$tunnel" 2>/dev/null
	# The browser identity the client saved for this tunnel. It is kept
	# across reboots on purpose, which is exactly why it has to go when the
	# thing that saved it does.
	rm -rf "/etc/qwdtt/$$tunnel"
done
rmdir /etc/qwdtt 2>/dev/null

exit 0
endef

# The counterpart to the zone that files/qwdtt.defaults writes at install: the
# zone, the forwarding it needs and the per-tunnel SNAT rules the up-script
# adds. Left behind, they describe devices that no longer exist and fw4 carries
# them through every reload, with nothing to say where they came from.
#
# The SNAT rules are matched the way they were written rather than by listing
# the tunnels, so one whose interface has since been renamed still goes.
define Package/qwdtt-client/postrm
#!/bin/sh
[ -n "$${IPKG_INSTROOT}" ] && exit 0

for section in $$(uci -q show firewall | \
	sed -n "s/^firewall\.\([^.=]*_snat\)\.src='qwdtt'$$/\1/p"); do
	uci -q delete "firewall.$$section"
done
uci -q delete firewall.qwdtt_lan
uci -q delete firewall.qwdtt
uci commit firewall
# restart, not reload: a reload rewrites the rules but leaves the departed
# zone's own chains in the live ruleset, empty and unreferenced.
[ ! -x /etc/init.d/firewall ] || /etc/init.d/firewall restart >/dev/null 2>&1

# The kill switch is the one leftover that does harm rather than merely sitting
# there. It is an unreachable default in the tunnel's own table, and with the
# tunnel gone that table holds nothing else, so the rule steering the LAN into
# it refuses everything - a router that loses its internet on uninstall, and
# again on every boot after, with no qwdtt left to explain it. The rule is kept:
# a rule whose table has no matching route falls through to the next one. So is
# the interface, which is where the credentials are.
#
# Found by its shape rather than by walking the qwdtt interfaces, because one
# whose interface has already been deleted belongs to no interface at all and
# was the case the walk missed.
for kill in $$(uci -q show network | \
	sed -n "s/^network\.\([^.=]*_killswitch\)=route$$/\1/p"); do
	[ "$$(uci -q get "network.$$kill.type")" = unreachable ] || continue
	[ "$$(uci -q get "network.$$kill.interface")" = loopback ] || continue
	uci -q delete "network.$$kill"
done
uci commit network
[ ! -x /etc/init.d/network ] || /etc/init.d/network reload >/dev/null 2>&1

exit 0
endef

$(eval $(call BuildPackage,qwdtt-client))
