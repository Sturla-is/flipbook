package config

const (
	ConsentRequired  = "required"
	ConsentAnonymous = "anonymous"
	ConsentDisabled  = "disabled"
)

func (c CRMConfig) HubSpotConfigured() bool {
	return c.HubSpot.Configured()
}

func (c CRMConfig) SalesforceConfigured() bool {
	return c.Salesforce.Configured() && c.Salesforce.InstanceURL != ""
}

func (c CRMConfig) MailchimpConfigured() bool {
	return c.Mailchimp.Configured() && c.Mailchimp.ListID != ""
}

func (c WebhookConfig) Configured() bool {
	return c.Enabled && c.TimeoutSeconds > 0 && c.MaxAttempts > 0 && c.RetryBackoffSeconds > 0
}

func (c OCRConfig) Configured() bool {
	return c.Enabled && c.TesseractBin != "" && c.Language != "" && c.TimeoutSeconds > 0
}
