# Emby Account Management

This context manages access lifecycles for user accounts on one Emby server through an administrative web application and integration-facing REST interface.

## Language

**Emby 用户**:
The user account that exists on the configured Emby server and is allowed or denied login by Emby.
_Avoid_: Emby account

**业务账号**:
The system record that owns an Emby 用户 association, its access state, expiry time, and administrative metadata. One business account corresponds to one Emby 用户.
_Avoid_: user, account

**订阅期**:
The access interval ending at a business account's expiry time. Redemption extends it from the existing expiry time rather than from the redemption time.
_Avoid_: membership, plan

**邀请码**:
A managed redeemable credential with a duration and usage constraints, usable for registration or renewal. It is not a payment instrument.
_Avoid_: coupon, payment code

**兑换**:
A successful, recorded use of an invite code to register a business account or extend its subscription period.
_Avoid_: payment, order

**禁用**:
The state in which login is denied by setting the linked Emby 用户's `IsDisabled` policy to true. It does not delete the user or their Emby data.
_Avoid_: deletion, removal
