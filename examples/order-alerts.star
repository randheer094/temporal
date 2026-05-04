# Example Starlark extraction script — copy to ~/.temporal/scripts/order-alerts.star
#
# This script is referenced by the `script_order_alerts` rule in examples/rules.yaml.
# It receives the full event body as a Starlark dict and returns a list of log entries.
#
# Use scripts when your extraction logic goes beyond what gjson templates can express:
# conditionals, string formatting, filtering across multiple fields, etc.
#
# Reload after editing: temporal server rules

def process(body):
    entries = []

    request = body.get("request", {})
    response = body.get("response", {})
    items = request.get("body", {}).get("items", [])
    status = response.get("statusCode", 0)

    for item in items:
        qty = item.get("qty", 0)
        sku = item.get("sku", "unknown")
        price = item.get("unitPrice", 0)

        # Flag high-value line items
        if qty * price > 500:
            entries.append({
                "type": "high_value_item",
                "title": sku,
                "message": [
                    "qty: " + str(qty),
                    "unit price: " + str(price),
                    "line total: " + str(qty * price),
                    "status: " + str(status),
                ],
            })

        # Flag low-stock items regardless of value
        if qty < 3:
            entries.append({
                "type": "low_stock_warning",
                "title": sku,
                "message": "only " + str(qty) + " ordered — may indicate low stock",
            })

    return entries
