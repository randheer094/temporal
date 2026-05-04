# Example Python extraction script — copy to ~/.temporal/scripts/order-alerts.py
#
# This script is referenced by the `script_order_alerts` rule in examples/rules.yaml.
# It receives the full event body as a Python dict and returns a list of log entries.
#
# Use scripts when your extraction logic goes beyond what gjson templates can express:
# conditionals, string formatting, JSON parsing of string-valued bodies, filtering
# across multiple fields, etc.
#
# Requires python3 (or python) on the daemon's PATH.
# Reload after editing: temporal server rules

import json


def process(body):
    entries = []

    request = body.get("request", {})
    response = body.get("response", {})

    # Proxyman forwards request/response bodies as raw strings; parse if needed.
    req_body = request.get("body", {})
    if isinstance(req_body, str):
        try:
            req_body = json.loads(req_body)
        except (TypeError, ValueError):
            req_body = {}

    items = req_body.get("items", [])
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
                    f"qty: {qty}",
                    f"unit price: {price}",
                    f"line total: {qty * price}",
                    f"status: {status}",
                ],
            })

        # Flag low-stock items regardless of value
        if qty < 3:
            entries.append({
                "type": "low_stock_warning",
                "title": sku,
                "message": f"only {qty} ordered — may indicate low stock",
            })

    return entries
