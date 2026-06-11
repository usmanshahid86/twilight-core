# Staking Omission

Twilight v1 omits `x/staking`, `x/distribution`, and `x/slashing` entirely.

This is structural enforcement, not an ante-handler blocklist:

- no staking keeper or store is constructed;
- no staking MsgServer or query service is registered;
- no staking genesis state exists;
- no staking EndBlocker exists;
- direct, authz-wrapped, governance, and internal routed staking mutations
  have no registered route.

The app test asserts that staking delegate and create-validator type URLs have
no handlers and that staking is absent from the module manager.

A future economic staking module must remain independent of validator
admission. A C1 read-only staking mirror may be added for explorer
compatibility, but it must be derived from coreslot and remain absent from the
EndBlock order.
