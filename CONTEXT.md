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

**销售方案**:
An administrator-configured purchasable duration and price for either issuing an activation code or extending an existing subscription. Changing a sale option does not change already-created orders.
_Avoid_: order, payment

**激活码**:
A one-time redeemable credential issued after a paid activation-code order succeeds. It can be used to register an Emby 用户 or, where supported, extend a subscription; it is distinct from the payment order that produced it.
_Avoid_: payment order, coupon

**续费方案**:
A paid sale option that extends one identified business account from its current subscription expiry after the payment is confirmed. It does not itself create an activation code.
_Avoid_: invite code

**支付订单**:
The local record linking one selected sale option to a payment-center checkout and its idempotent fulfillment. It also keeps a buyer-information snapshot when the purchaser provides one; for renewal orders the business account username is the buyer reference. Payment confirmation is separate from whether the activation code or renewal has been delivered.
_Avoid_: payment receipt, invite code

**兑换**:
A successful, recorded use of an invite code to register a business account or extend its subscription period.
_Avoid_: payment, order

**用户中心会话**:
A server-side authenticated session that identifies one 业务账号. When the session is valid, renewal uses that account identity rather than trusting a username or password submitted by the browser.
_Avoid_: hidden account field

**禁用**:
The state in which login is denied by setting the linked Emby 用户's `IsDisabled` policy to true. It does not delete the user or their Emby data.
_Avoid_: deletion, removal
