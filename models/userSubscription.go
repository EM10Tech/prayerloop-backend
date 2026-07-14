package models

import "time"

// UserSubscription is the cached copy of a user's RevenueCat entitlement
// state (prayerloop-psql migration 029_add_subscription_tracking). One row
// per user, upserted from webhook (and, later, /sync) events — never queried
// live against the RevenueCat API on the request path. A missing row means
// free tier; a row is not required to exist for every user.
type UserSubscription struct {
	User_Subscription_ID   int        `json:"userSubscriptionId" goqu:"skipinsert"`
	User_Profile_ID        int        `json:"userProfileId"`
	Is_Premium             bool       `json:"isPremium"`
	Entitlement_ID         *string    `json:"entitlementId"`
	Product_ID             *string    `json:"productId"`
	Store                  *string    `json:"store"`
	Period_Type            *string    `json:"periodType"`
	Expires_At             *time.Time `json:"expiresAt"`
	Will_Renew             *bool      `json:"willRenew"`
	Revenuecat_App_User_ID string     `json:"revenuecatAppUserId"`
	Last_Event_Type        *string    `json:"lastEventType"`
	Last_Event_At          *time.Time `json:"lastEventAt"`
	Datetime_Create        time.Time  `json:"datetimeCreate" goqu:"skipinsert"`
	Datetime_Update        time.Time  `json:"datetimeUpdate" goqu:"skipinsert"`
}
