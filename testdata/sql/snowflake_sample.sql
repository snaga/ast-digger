-- Snowflake Production Batch Pipeline: Daily Sales & Customer Mart Aggregation
-- Dialect: Snowflake SQL
-- Features: Multi-CTE DAG, QUALIFY clause, CONVERT_TIMEZONE, INSERT INTO statement

WITH
raw_orders AS (
    -- Ingest raw sales order transactions from upstream lake stage
    SELECT
        order_id,
        user_id,
        order_timestamp,
        order_status,
        currency,
        amount,
        tax_amount,
        discount_amount,
        payment_method,
        shipping_address,
        metadata
    FROM source_db.lake.orders
    WHERE order_timestamp >= DATEADD('day', -7, CURRENT_TIMESTAMP())
      AND user_id NOT IN (
          SELECT user_id
          FROM source_db.lake.blacklisted_users
          WHERE is_fraudulent = TRUE
      )
),

cleaned_orders AS (
    -- Deduplicate order events and filter out cancelled/test records
    SELECT
        order_id,
        user_id,
        CONVERT_TIMEZONE('UTC', 'Asia/Tokyo', order_timestamp) AS order_jst_time,
        order_status,
        UPPER(currency) AS currency,
        ROUND(amount, 2) AS amount,
        ROUND(tax_amount, 2) AS tax_amount,
        ROUND(COALESCE(discount_amount, 0), 2) AS discount_amount,
        payment_method,
        metadata
    FROM raw_orders
    WHERE order_status NOT IN ('CANCELLED', 'TEST', 'VOID')
    QUALIFY ROW_NUMBER() OVER (
        PARTITION BY order_id
        ORDER BY order_timestamp DESC
    ) = 1
),

user_master AS (
    -- User profile master with active tier mapping
    SELECT
        user_id,
        user_name,
        email,
        membership_tier,
        country_code,
        created_at,
        is_active
    FROM source_db.lake.users
    WHERE is_active = TRUE
),

joined_sales AS (
    -- Join cleansed orders with user master to enrich attributes
    SELECT
        sub_items.order_id,
        sub_items.user_id,
        u.user_name,
        u.membership_tier,
        u.country_code,
        sub_items.order_jst_time,
        DATE_TRUNC('day', sub_items.order_jst_time) AS sales_date,
        sub_items.currency,
        sub_items.amount,
        sub_items.tax_amount,
        sub_items.discount_amount,
        (sub_items.amount + sub_items.tax_amount - sub_items.discount_amount) AS net_sales_amount,
        (SELECT MAX(bonus_multiplier) FROM source_db.lake.tier_bonuses b WHERE b.tier = u.membership_tier) AS bonus_multiplier
    FROM (
        SELECT *
        FROM cleaned_orders
        WHERE amount > 0
    ) AS sub_items
    INNER JOIN user_master u ON sub_items.user_id = u.user_id
),

tax_calculation AS (
    -- Compute daily aggregated financial and tax totals per country & tier
    SELECT
        sales_date,
        country_code,
        membership_tier,
        COUNT(DISTINCT order_id) AS total_orders,
        COUNT(DISTINCT user_id) AS unique_buyers,
        SUM(amount) AS total_gross_amount,
        SUM(tax_amount) AS total_tax_amount,
        SUM(discount_amount) AS total_discount_amount,
        SUM(net_sales_amount) AS total_net_sales
    FROM joined_sales
    GROUP BY sales_date, country_code, membership_tier
)

-- Insert aggregated marts records into final analytics table
INSERT INTO marts.daily_sales_summary (
    sales_date,
    country_code,
    membership_tier,
    total_orders,
    unique_buyers,
    total_gross_amount,
    total_tax_amount,
    total_discount_amount,
    total_net_sales
)
SELECT
    sales_date,
    country_code,
    membership_tier,
    total_orders,
    unique_buyers,
    total_gross_amount,
    total_tax_amount,
    total_discount_amount,
    total_net_sales
FROM tax_calculation
WHERE sales_date >= (SELECT MIN(sales_date) FROM tax_calculation);
