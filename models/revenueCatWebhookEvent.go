package models

import "time"

// RevenueCatWebhookPayload is the top-level envelope RevenueCat POSTs to
// /webhooks/revenuecat: {"api_version": "1.0", "event": {...}}.
type RevenueCatWebhookPayload struct {
	Api_Version string                 `json:"api_version"`
	Event       RevenueCatWebhookEvent `json:"event"`
}

// RevenueCatWebhookEvent doubles as both the webhook payload DTO (unmarshaled
// from the "event" object of an incoming delivery) and the row shape of the
// revenuecat_webhook_event idempotency/audit table (prayerloop-psql migration
// 029_add_subscription_tracking) — default goqu column-rename lowercases
// field names, which already matches this table's snake_case columns for the
// fields that are persisted as their own column. The remaining fields below
// are parsed only to derive the user_subscription upsert; the full raw event
// is archived verbatim in Payload/payload for audit/replay instead of being
// reconstructed from these fields.
type RevenueCatWebhookEvent struct {
	Revenuecat_Webhook_Event_ID int       `json:"-" goqu:"skipinsert"`
	Event_ID                    string    `json:"id"`
	Event_Type                  string    `json:"type"`
	App_User_ID                 string    `json:"app_user_id"`
	Payload                     string    `json:"-"`
	Processed                   bool      `json:"-" goqu:"skipinsert"`
	Received_At                 time.Time `json:"-" goqu:"skipinsert"`

	Product_ID         string   `json:"product_id"`
	Entitlement_IDs    []string `json:"entitlement_ids"`
	Period_Type        string   `json:"period_type"`
	Purchased_At_Ms    int64    `json:"purchased_at_ms"`
	Expiration_At_Ms   *int64   `json:"expiration_at_ms"`
	Event_Timestamp_Ms int64    `json:"event_timestamp_ms"`
	Environment        string   `json:"environment"`
	Store              string   `json:"store"`
	Cancel_Reason      string   `json:"cancel_reason"`
	Expiration_Reason  string   `json:"expiration_reason"`
}
