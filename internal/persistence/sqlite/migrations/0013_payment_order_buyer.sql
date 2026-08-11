ALTER TABLE payment_orders ADD COLUMN buyer_info TEXT NOT NULL DEFAULT '';

CREATE INDEX idx_payment_orders_buyer_info ON payment_orders(buyer_info, created_at DESC);
