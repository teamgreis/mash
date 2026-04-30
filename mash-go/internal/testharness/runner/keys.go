package runner

// ============================================================================
// Precondition keys -- used in preconditionKeyLevels and simulationPreconditionKeys.
// ============================================================================

// Level 0: Always-true environment preconditions.
const (
	PrecondDeviceBooted      = "device_booted"
	PrecondControllerRunning = "controller_running"
	PrecondDeviceInNetwork   = "device_in_network"
	PrecondDeviceListening   = "device_listening"
)

// D2D simulation preconditions.
const (
	PrecondTwoDevicesSameZone          = "two_devices_same_zone"
	PrecondTwoDevicesDifferentZones    = "two_devices_different_zones"
	PrecondDeviceBCertExpired          = "device_b_cert_expired"
	PrecondTwoDevicesSameDiscriminator = "two_devices_same_discriminator"
)

// Device state preconditions.
const (
	PrecondDeviceReset                 = "device_reset"
	PrecondDeviceHasGridZone           = "device_has_grid_zone"
	PrecondDeviceHasLocalZone          = "device_has_local_zone"
	PrecondDeviceInLocalZone           = "device_in_local_zone"
	PrecondSessionPreviouslyConnected  = "session_previously_connected"
	PrecondFreshCommission             = "fresh_commission"
	PrecondDeviceHasOneZone            = "device_has_one_zone"
	PrecondDeviceHasAvailableZoneSlot  = "device_has_available_zone_slot"
)

// IPv6 / multi-interface preconditions.
const (
	PrecondDeviceHasGlobalAndLinkLocal = "device_has_global_and_link_local"
	PrecondDeviceHasLinkLocal          = "device_has_link_local"
	PrecondDeviceHasMultipleInterfaces = "device_has_multiple_interfaces"
)

// State-machine simulation preconditions (require commissioned session).
const (
	PrecondControlState        = "control_state"
	PrecondInitialControlState = "initial_control_state"
	PrecondProcessState        = "process_state"
	PrecondProcessCapable      = "process_capable"
	PrecondDeviceIsPausable    = "device_is_pausable"
	PrecondDeviceNotPausable   = "device_not_pausable"
	PrecondDeviceIsStoppable   = "device_is_stoppable"
	PrecondFailsafeDurationShort = "failsafe_duration_short"
)

// Controller preconditions.
const (
	PrecondZoneCreated              = "zone_created"
	PrecondControllerHasCert        = "controller_has_cert"
	PrecondControllerCertNearExpiry = "controller_cert_near_expiry"
)

// Zone management test preconditions (runner-side zone state).
const (
	PrecondNoZonesConfigured      = "no_zones_configured"
	PrecondLocalZoneConfigured    = "local_zone_configured"
	PrecondTwoZonesConfigured     = "two_zones_configured"
	PrecondSubscriptionActive     = "subscription_active"
	PrecondZoneCount              = "zone_count"
	PrecondZoneCountAtLeast       = "zone_count_at_least"
	PrecondNoOtherZonesConnected  = "no_other_zones_connected"
	PrecondAcceptsSetpoints       = "accepts_setpoints"
	PrecondTwoZonesWithLimits     = "two_zones_with_limits"
	PrecondSecondZoneConnected    = "second_zone_connected"
	PrecondNoExistingLimits       = "no_existing_limits"
	PrecondLimitIsSet             = "limit_is_set"
	PrecondZoneHasSetValues       = "zone_has_set_values"
	PrecondDeviceSupportsProduction = "device_supports_production"
	PrecondDeviceIsBidirectional  = "device_is_bidirectional"
	PrecondDeviceSupportsAsymmetric = "device_supports_asymmetric"
	PrecondEVConnected            = "ev_connected"
	PrecondEVPluggedIn            = "ev_plugged_in"
)

// Environment/negative-test preconditions.
const (
	PrecondDeviceZonesFull              = "device_zones_full"
	PrecondNoDevicesAdvertising         = "no_devices_advertising"
	PrecondDeviceSRVPresent             = "device_srv_present"
	PrecondDeviceAAAAMissing            = "device_aaaa_missing"
	PrecondDeviceAddressValid           = "device_address_valid"
	PrecondDevicePortClosed             = "device_port_closed"
	PrecondDeviceWillAppearAfterDelay   = "device_will_appear_after_delay"
	PrecondFiveZonesConnected           = "five_zones_connected"
	PrecondTwoZonesConnected            = "two_zones_connected"
	PrecondDeviceInZone                 = "device_in_zone"
	PrecondDeviceInTwoZones             = "device_in_two_zones"
	PrecondMultipleDevicesCommissioning = "multiple_devices_commissioning"
	PrecondMultipleDevicesCommissioned  = "multiple_devices_commissioned"
	PrecondMultipleControllersRunning   = "multiple_controllers_running"
)

// Connection/commissioning preconditions.
const (
	PrecondDeviceInCommissioningMode  = "device_in_commissioning_mode"
	PrecondDeviceUncommissioned       = "device_uncommissioned"
	PrecondCommissioningWindowOpen    = "commissioning_window_open"
	PrecondCommissioningWindowClosed  = "commissioning_window_closed"
	PrecondCommissioningWindowAt95s   = "commissioning_window_at_95s"
	PrecondDeviceConnected            = "device_connected"
	PrecondTLSConnectionEstablished   = "tls_connection_established"
	PrecondConnectionEstablished      = "connection_established"
	PrecondDeviceCommissioned         = "device_commissioned"
	PrecondSessionEstablished         = "session_established"
)

// ============================================================================
// State keys -- used with state.Set/state.Get for internal runner state.
// Many of these also appear as output map keys (handled by Key* constants
// below). When a string is used ONLY in state.Set/Get and never in output
// maps, it gets a State* constant. When it appears in both, the Key*
// constant is used for both purposes.
// ============================================================================

const (
	// Connection/session state.
	StateConnection     = "connection"
	StatePasePending    = "pase_pending"
	StateSessionKey     = "session_key"
	StateSessionKeyLen  = "session_key_length"
	StateDeviceID       = "device_id"
	StatePongSeq        = "pong_seq"
	StateUnsubscribedID = "unsubscribed_id"

	// Security state.
	StateLastErrorType         = "last_error_type"
	StateLastErrorDelayMs      = "last_error_delay_ms"
	StateLastResponseDelayMs   = "last_response_delay_ms"
	StateLastPaseAttemptsCount = "last_pase_attempts_count"
	StateLastPaseDelays        = "last_pase_delays"
	StateMeanDifferenceMs      = "mean_difference_ms"
	StateDistributionsOverlap  = "distributions_overlap"

	// Timing state.
	StateSlowExchangeDelayMs = "slow_exchange_delay_ms"
	StateSlowExchangeStart   = "slow_exchange_start"
	StateConnectDurationMs   = "connect_duration_ms"

	// Certificate state.
	StateCertDaysUntilExpiry = "cert_days_until_expiry"
	StateCertExpired         = "cert_expired"
	StateCertSequence        = "cert_sequence"
	StateRenewalNonce        = "renewal_nonce"
	StatePendingCSR          = "pending_csr"
	StateCSRHistory          = "csr_history" // [][]byte - all CSRs received
	StateRenewalComplete     = "renewal_complete"
	StateReceivedEvent       = "received_event"
	StateGracePeriodDays     = "grace_period_days"
	StateDaysPastExpiry      = "days_past_expiry"
	StateInGracePeriod       = "in_grace_period"
	StateGracePeriodExpired  = "grace_period_expired"

	// Subscription state.
	StateSubscriptionID         = "subscription_id"
	StateRecordedSubscriptionID = "recorded_subscription_id"
	StateSavedSubscriptionID    = "_saved_subscription_id"
	StateSubscribedEndpointID   = "_subscribed_endpoint_id"
	StateSubscribedFeatureID    = "_subscribed_feature_id"
	StateSubscribedAttributes   = "_subscribed_attributes"

	// Network state.
	StateNetworkPartitioned = "network_partitioned"
	StateNetworkFilter      = "network_filter"
	StateInterfaceUp        = "interface_up"
	StateKeepaliveIdle      = "_keepalive_idle"
	StateKeepaliveIdleSec   = "_keepalive_idle_sec"
	StateGracefullyClosed   = "_gracefully_closed"
	StateDeviceAddress      = "device_address"
	StateClockOffsetMs      = "clock_offset_ms"

	// Device state.
	StateDeviceConnected   = "device_connected"
	StateOperatingState    = "operating_state"
	StateControlState      = "control_state"
	StateProcessState      = "process_state"
	StateActiveFaultCount  = "active_fault_count"
	StateFailsafeLimit     = "failsafe_limit"
	StateEVConnected       = "ev_connected"
	StateEVChargeRequested = "ev_charge_requested"
	StateCablePluggedIn    = "cable_plugged_in"
	StateLastTrigger       = "last_trigger"

	// Discovery state.
	StatePairingRequestDiscriminator = "pairing_request_discriminator"
	StatePairingRequestZoneID        = "pairing_request_zone_id"
	StateDeviceWasRemoved            = "device_was_removed"
	StateCommissioningActive         = "commissioning_active"
	StateCommissioningCompleted      = "commissioning_completed"
	StateCommWindowStart             = "commissioning_window_start"
	StateOperationalConnEstablished  = "operational_connection_established"

	// Cert handler state.
	StateExtractedDeviceID = "extracted_device_id"

	// Commissioned zone IDs (set by preconditions after commissioning).
	StateGridZoneID    = "grid_zone_id"
	StateLocalZoneID   = "local_zone_id"
	StateTestZoneID    = "test_zone_id"
	StateCurrentZoneID = "current_zone_id"
	StateOtherZoneID   = "other_zone_id"
	StateRemovedZoneIDs = "removed_zone_ids"

	// Setup.
	StateSetupCode           = "setup_code"
	StateDeviceDiscriminator = "device_discriminator"
)

// ============================================================================
// Output/shared keys -- used in handler return maps (map[string]any{...})
// and often also stored in state. These form the public interface between
// handlers and YAML test expectations.
// ============================================================================

// Connection output keys.
const (
	KeyConnectionEstablished = "connection_established"
	KeyConnected             = "connected"
	KeyDisconnected          = "disconnected"
	KeyTLSHandshakeSuccess   = "tls_handshake_success"
	KeyTarget                = "target"
	KeyError                 = "error"
	KeyErrorCode             = "error_code"
	KeyErrorDetail           = "error_detail"
	KeyTLSError              = "tls_error"
	KeyTLSAlert              = "tls_alert"
	KeyTLSVersion            = "tls_version"
	KeyNegotiatedVersion     = "negotiated_version"
	KeyNegotiatedCipher      = "negotiated_cipher"
	KeyNegotiatedGroup       = "negotiated_group"
	KeyNegotiatedProtocol    = "negotiated_protocol"
	KeyNegotiatedALPN        = "negotiated_alpn"
	KeyMutualAuth            = "mutual_auth"
	KeyChainValidated        = "chain_validated"
	KeySelfSignedAccepted    = "self_signed_accepted"
	KeyServerCertCNPrefix    = "server_cert_cn_prefix"
	KeyServerCertSelfSigned  = "server_cert_self_signed"
	KeyHasPeerCerts            = "has_peer_certs"
	KeyControllerCertVerified  = "controller_cert_verified"
	KeyState                   = "state"
)

// TLS introspection output keys.
const (
	KeyFullHandshake         = "full_handshake"
	KeyPSKUsed               = "psk_used"
	KeyEarlyDataAccepted     = "early_data_accepted"
	KeySessionTicketReceived = "session_ticket_received"
)

// Session/PASE output keys.
const (
	KeySessionEstablished        = "session_established"
	KeyCommissionSuccess         = "commission_success"
	KeyCorrectDeviceCommissioned = "correct_device_commissioned"
	KeyKeyLength                 = "key_length"
	KeyKeyNotZero                = "key_not_zero"
	KeyRequestSent               = "request_sent"
	KeyPAGenerated               = "pA_generated"
	KeyResponseReceived          = "response_received"
	KeyPBReceived                = "pB_received"
	KeyConfirmSent               = "confirm_sent"
	KeyVerifyReceived            = "verify_received"
)

// Protocol operation output keys.
const (
	KeyReadSuccess      = "read_success"
	KeyWriteSuccess     = "write_success"
	KeySubscribeSuccess = "subscribe_success"
	KeyInvokeSuccess    = "invoke_success"
	KeyResponse         = "response"
	KeyValue            = "value"
	KeyStatus           = "status"
	KeyResult           = "result"
	KeySubscriptionID   = "subscription_id"
)

// Utility output keys.
const (
	KeyWaited = "waited"
)

// Zone output keys.
const (
	KeyZoneID              = "zone_id"
	KeySaveZoneID          = "save_zone_id"
	KeyLogicalZoneSet      = "logical_zone_set"
	KeyZoneCreated         = "zone_created"
	KeyZoneType            = "zone_type"
	KeyFingerprint         = "fingerprint"
	KeyZoneIDPresent       = "zone_id_present"
	KeyZoneIDLength        = "zone_id_length"
	KeyDeviceAdded         = "device_added"
	KeyZoneRemoved         = "zone_removed"
	KeyZoneDeleted         = "zone_deleted"
	KeyZoneFound           = "zone_found"
	KeyZoneMetadata          = "zone_metadata"
	KeyDeviceCount           = "device_count"
	KeyCommissionedAtRecent  = "commissioned_at_recent"
	KeyLastSeenRecent        = "last_seen_recent"
	KeyLastSeenNotUpdated    = "last_seen_not_updated"
	KeyZoneExists          = "zone_exists"
	KeyZones               = "zones"
	KeyZoneCount           = "zone_count"
	KeyZoneIDsInclude      = "zone_ids_include"
	KeyCount               = "count"
	KeyMetadata            = "metadata"
	KeyCAValid             = "ca_valid"
	KeyPathLength          = "path_length"
	KeyAlgorithm           = "algorithm"
	KeyBasicConstraintsCA  = "basic_constraints_ca"
	KeyValidityYearsMin    = "validity_years_min"
	KeyBindingValid        = "binding_valid"
	KeyDerivationValid     = "derivation_valid"
	KeyZoneDisconnected    = "zone_disconnected"
	KeyBidirectionalActive = "bidirectional_active"
	KeySequenceRestored    = "sequence_restored"
	KeySubscriptionsFirst  = "subscriptions_first"
	KeyCommandReplayed     = "command_replayed"
	KeyAllSubscribed       = "all_subscribed"
	KeyOrderPreserved      = "order_preserved"
	KeyTLSActive           = "tls_active"
	KeyVersionMatches      = "version_matches"
)

// Raw wire extended output keys.
const (
	KeyResponseMessageID      = "response_message_id"
	KeyConnectionOpen         = "connection_open"
	KeyConnectionError        = "connection_error"
	KeyAllResponsesReceived   = "all_responses_received"
	KeyAllCorrelationsCorrect = "all_correlations_correct"
	KeyEachMessageIDMatches   = "each_message_id_matches"
	KeyAllPairsMatched        = "all_pairs_matched"
	KeyChangedAttribute       = "changed_attribute"
	KeyErrorMessageContains   = "error_message_contains"
	KeyMessageIDsMatch        = "message_ids_match"
	KeyTimeoutAfter           = "timeout_after"
	KeyTimeoutDetected        = "timeout_detected"
	KeyResultStatus           = "result_status"
)

// Connection handler output keys.
const (
	KeyConnectDurationMs       = "connect_duration_ms"
	KeyCloseSent               = "close_sent"
	KeySimultaneous            = "simultaneous"
	KeyReconnectCancelled      = "reconnect_cancelled"
	KeyMonitoringStarted       = "monitoring_started"
	KeyMonitoringBackoff       = "monitoring_backoff"
	KeyPongReceived            = "pong_received"
	KeyLatencyUnder            = "latency_under"
	KeyPongSeq                 = "pong_seq"
	KeyAllPongsReceived        = "all_pongs_received"
	KeyAverageLatencyUnder     = "average_latency_under"
	KeyKeepaliveActive         = "keepalive_active"
	KeyRawSent                 = "raw_sent"
	KeyParseSuccess            = "parse_success"
	KeyErrorStatus             = "error_status"
	KeyRawBytesSent            = "raw_bytes_sent"
	KeyAlertSent               = "alert_sent"
	KeyPeerCloseNotify         = "peer_close_notify"
	KeyCommandQueued           = "command_queued"
	KeyQueueLength             = "queue_length"
	KeyResultReceived          = "result_received"
	KeyQueueEmpty              = "queue_empty"
	KeyAction                  = "action"
	KeyQueueRemaining          = "queue_remaining"
	KeyMessagesSent            = "messages_sent"
	KeyReadCount               = "read_count"
	KeyResults                 = "results"
	KeyDisconnectedAfterInvoke = "disconnected_after_invoke"
	KeyCloseAckReceived        = "close_ack_received"
	KeyCloseAcknowledged       = "close_acknowledged"
	KeyPendingResponseReceived = "pending_response_received"
	KeyBothCloseReceived       = "both_close_received"
	KeyCloseReason             = "close_reason"
	KeyPingSent                = "ping_sent"
	KeySequenceMatch           = "sequence_match"
	KeySubscribeCount          = "subscribe_count"
	KeySubscriptionCount       = "subscription_count"
	KeySubscriptionIDReturned  = "subscription_id_returned"
	KeySubscriptionIDsUnique   = "subscription_ids_unique"
	KeyAllSucceed              = "all_succeed"
	KeySuccessCount            = "success_count"
	KeyFailureCount            = "failure_count"
	KeySubscriptions           = "subscriptions"
	KeyUnsubscribed            = "unsubscribed"
	KeyNotificationReceived    = "notification_received"
	KeyNotificationData        = "notification_data"
	KeyNotificationsReceived   = "notifications_received"
	KeyAllReceived             = "all_received"
	KeyPrimingReceived         = "priming_received"
	KeySavePrimingValue        = "save_priming_value"
	KeyIsPrimingReport         = "is_priming_report"
	KeyIsDelta                 = "is_delta"
	KeyIsHeartbeat             = "is_heartbeat"
	KeyContainsAllAttributes   = "contains_all_attributes"
	KeyContainsFullState       = "contains_full_state"
	KeySubscriptionIDSaved     = "subscription_id_saved"
	KeyContainsOnly            = "contains_only"
	KeyUnsubscribeSuccess      = "unsubscribe_success"
	KeyAllIDsMatchSubs         = "all_ids_match_subscriptions"
	KeyReceivedCount           = "received_count"
	KeyAllIDsUnique            = "all_ids_unique"
	KeyNotificationCount       = "notification_count"
)

// Security handler output keys.
const (
	KeyConnectionRejected       = "connection_rejected"
	KeyRejectionAtTLSLevel      = "rejection_at_tls_level"
	KeyConnectionIndex          = "connection_index"
	KeyConnectionClosed         = "connection_closed"
	KeyIndex                    = "index"
	KeyFloodCompleted           = "flood_completed"
	KeyDeviceRemainsResponsive  = "device_remains_responsive"
	KeyAcceptedConnections      = "accepted_connections"
	KeyMaxAcceptedConnections   = "max_accepted_connections"
	KeyRejectedConnections      = "rejected_connections"
	KeyCommissionable           = "commissionable"
	KeyAdvertisementFound       = "advertisement_found"
	KeyConnectionType           = "connection_type"
	KeyCommissioningModeEntered      = "commissioning_mode"
	KeyCommissioningModeEnteredAlias = "commissioning_mode_entered"
	KeyCommissioningModeExited       = "commissioning_mode_exited"
	KeyCNMismatchWarning        = "cn_mismatch_warning"
	KeyReconnectionSuccessful   = "reconnection_successful"
	KeySlowExchangeStarted      = "slow_exchange_started"
	KeyDelayMs                  = "delay_ms"
	KeyConnectionClosedByDevice = "connection_closed_by_device"
	KeyTotalDurationMs          = "total_duration_ms"
	KeyAttemptsMade             = "attempts_made"
	KeyAllResponsesImmediate    = "all_responses_immediate"
	KeyMaxDelayMs               = "max_delay_ms"
	KeyResponseDelayMs          = "response_delay_ms"
	KeyAttemptFailed            = "attempt_failed"
	KeyErrorName                = "error_name"
	KeyHandshakeError           = "handshake_error"
	KeyMeanRecorded             = "mean_recorded"
	KeyMeanMs                   = "mean_ms"
	KeyStddevMs                 = "stddev_ms"
	KeySamplesCollected         = "samples_collected"
	KeyMeanDifferenceMs         = "mean_difference_ms"
	KeyDistributionsOverlap     = "distributions_overlap"
	KeyPubkeyMeanMs             = "pubkey_mean_ms"
	KeyPasswordMeanMs           = "password_mean_ms"
	KeyBusyResponseReceived     = "busy_response_received"
	KeyBusyErrorCode            = "busy_error_code"
	KeyBusyRetryAfterPresent    = "busy_retry_after_present"
	KeyBusyRetryAfterValue      = "busy_retry_after_value"
	KeyBusyRetryAfter           = "busy_retry_after"
	KeyPaseFailed               = "pase_failed"
	KeyTLSHandshakeFailed       = "tls_handshake_failed"
	KeyActiveConnections        = "active_connections"
	KeyConnectionsOpened        = "connections_opened"
)

// Discovery handler output keys.
const (
	KeyDeviceFound              = "device_found"
	KeyServiceCount             = "service_count"
	KeyServices                 = "services"
	KeyTXTRecords               = "txt_records"
	KeyDevicesFound             = "devices_found"
	KeyDevicesFoundMin          = "devices_found_min"
	KeyRetriesPerformedMin      = "retries_performed_min"
	KeyInstanceConflictResolved = "instance_conflict_resolved"
	KeyInstanceName             = "instance_name"
	KeyInstancesForDevice       = "instances_for_device"
	KeyInstanceNamePrefix       = "instance_name_prefix"
	KeyZoneIDLengthDisc         = "zone_id_length"
	KeyDeviceIDLength           = "device_id_length"
	KeyZoneIDHexValid           = "zone_id_hex_valid"
	KeyDeviceIDHexValid         = "device_id_hex_valid"
	KeyInstanceNameFormat       = "instance_name_format"
	KeyTXTZILength              = "txt_ZI_length"
	KeyTXTDRange                = "txt_D_range"
	KeyTXTFound                 = "txt_found"
	KeyHost                     = "host"
	KeyPort                     = "port"
	KeyAdvertising              = "advertising"
	KeyNotAdvertising           = "not_advertising"
	KeyBrowsing                 = "browsing"
	KeyNotBrowsing              = "not_browsing"
	KeyQRPayload                = "qr_payload"
	KeyValid                    = "valid"
	KeyDiscriminator            = "discriminator"
	KeySetupCode                = "setup_code"
	KeyPairingRequestAnnounced  = "pairing_request_announced"
	KeyAnnouncementSent         = "announcement_sent"
	KeyZoneName                 = "zone_name"
	KeyZonePriority             = "zone_priority"
	KeyCreatedAtRecent          = "created_at_recent"
	KeyDiscoveryStarted         = "discovery_started"
	KeyDiscoveryStopped         = "discovery_stopped"
	KeyDeviceHasTXTRecords      = "device_has_txt_records"
	KeyTXTValid                 = "txt_valid"
	KeyAllResultsInZone         = "all_results_in_zone"
	KeyAAAACount                = "aaaa_count"
	KeyAAAACountMin             = "aaaa_count_min"
	KeyKeyExists                = "key_exists"
	KeySRVHostnameValid         = "srv_hostname_valid"
	KeyIPv6Valid                 = "ipv6_valid"
	KeyHasGlobalOrULA           = "has_global_or_ula"
	KeyHasLinkLocal             = "has_link_local"
	KeyConnectedAddressType     = "connected_address_type"
	KeyNewAddressAnnounced      = "new_address_announced"
	KeyAddressesFromMultipleIFs = "addresses_from_multiple_interfaces"
	KeyInterfaceCorrect         = "interface_correct"
	KeyRecordsUnchanged         = "records_unchanged"
	KeyWindowExpiryWarning      = "window_expiry_warning"
)

// Renewal handler output keys.
const (
	KeyNonceGenerated                = "nonce_generated"
	KeyNonceLength                   = "nonce_length"
	KeyNonceHashPresent              = "nonce_hash_present"
	KeyNonceHashMatchesExpected      = "nonce_hash_matches_expected"
	KeyCSRReceived                   = "csr_received"
	KeyCSRValid                      = "csr_valid"
	KeyCSRLength                     = "csr_length"
	KeyStatusName                    = "status_name"
	KeyCertSent                      = "cert_sent"
	KeySequenceIncremented           = "sequence_incremented"
	KeyNewSequence                   = "new_sequence"
	KeyAckReceived                   = "ack_received"
	KeyActiveSequence                = "active_sequence"
	KeyNewCertActive                 = "new_cert_active"
	KeyRenewalComplete               = "renewal_complete"
	KeySubscriptionIDRecorded        = "subscription_id_recorded"
	KeySameSubscriptionID            = "same_subscription_id"
	KeySubscriptionActive            = "subscription_active"
	KeySameConnection                = "same_connection"
	KeyNoReconnectionRequired        = "no_reconnection_required"
	KeyOperationalConnectionActive   = "operational_connection_active"
	KeyMutualTLS                     = "mutual_tls"
	KeyPasePerformed                 = "pase_performed"
	KeyCommissioningConnectionClosed = "commissioning_connection_closed"
	KeyCertExpirySet                 = "cert_expiry_set"
	KeyDaysUntilExpiry               = "days_until_expiry"
	KeyEventType                     = "event_type"
	KeyExpiresAtPresent              = "expires_at_present"
	KeyDaysRemainingValid            = "days_remaining_valid"
	KeyConnectionFailed              = "connection_failed"
	KeyErrorType                     = "error_type"
	KeyGracePeriodSet                = "grace_period_set"
	KeyGraceDays                     = "grace_days"
	KeyTimeAdvanced                  = "time_advanced"
	KeyDaysRemaining                 = "days_remaining"
)

// Cert handler output keys.
const (
	KeyCertValid           = "cert_valid"
	KeyChainValid          = "chain_valid"
	KeyNotExpired          = "not_expired"
	KeyHasCerts            = "has_certs"
	KeySubjectMatches      = "subject_matches"
	KeyCommonName          = "common_name"
	KeyHasOperationalCert  = "has_operational_cert"
	KeyCertSignedByZoneCA  = "cert_signed_by_zone_ca"
	KeyCertValidityDays    = "cert_validity_days"
	KeyCertStoreValid          = "cert_store_valid"
	KeyCertCount               = "cert_count"
	KeyCommonNameIsDeviceID    = "common_name_is_device_id"
	KeyDeviceHasDeviceID       = "device_has_device_id"
	KeyDeviceIDMatchesCertCN   = "device_id_matches_cert_cn"
	KeyZoneCAStored            = "zone_ca_stored"
	KeyOperationalCertStored   = "operational_cert_stored"
	KeyDeviceID            = "device_id"
	KeyDeviceIDPresent     = "device_id_present"
	KeyExtracted           = "extracted"
	KeyCommissioningState  = "commissioning_state"
	KeyStateMatches        = "state_matches"
	KeyPASEReset           = "pase_reset"
	KeyPASEXSent           = "pase_x_sent"
	KeyPeerValid           = "peer_valid"
	KeyVerificationSuccess = "verification_success"
	KeySameZoneCA          = "same_zone_ca"
)

// Controller handler output keys.
const (
	KeyActionTriggered    = "action_triggered"
	KeyCommissioned       = "commissioned"
	KeyControllerID       = "controller_id"
	KeyCertPresent        = "cert_present"
	KeySignedByZoneCA     = "signed_by_zone_ca"
	KeyIssuerFingerprint  = "issuer_fingerprint"
	KeyValidityDaysMin    = "validity_days_min"
	KeyStateValid         = "state_valid"
	KeyDurationSet        = "duration_set"
	KeyMinutes            = "minutes"
	KeyDurationSeconds    = "duration_seconds"
	KeyDurationSecondsMin = "duration_seconds_min"
	KeyDurationSecondsMax = "duration_seconds_max"
	KeyDeviceRemoved      = "device_removed"
	KeyRenewalSuccess     = "renewal_success"
	KeyRenewalInitiated   = "renewal_initiated"
	KeyRestarted          = "restarted"
)

// Device handler output keys.
const (
	KeyValueSet                = "value_set"
	KeyKey                     = "key"
	KeyValuesSet               = "values_set"
	KeyRapid                   = "rapid"
	KeyTriggered               = "triggered"
	KeyDeviceConfigured        = "device_configured"
	KeyConfigurationSuccess    = "configuration_success"
	KeyExposedDeviceConfigured = "exposed_device_configured"
	KeyAttributeUpdated        = "attribute_updated"
	KeyStateChanged            = "state_changed"
	KeyDetailSet               = "detail_set"
	KeyFaultTriggered          = "fault_triggered"
	KeyFaultCode               = "fault_code"
	KeyFaultCleared            = "fault_cleared"
	KeyActiveFaults            = "active_faults"
	KeyEVConnected             = "ev_connected"
	KeyCablePluggedIn          = "cable_plugged_in"
	KeyEVDisconnected          = "ev_disconnected"
	KeyChargeRequested         = "charge_requested"
	KeyCableUnplugged          = "cable_unplugged"
	KeyOverrideActive          = "override_active"
	KeyFactoryReset            = "factory_reset"
	KeyPowerCycled             = "power_cycled"
	KeyPoweredOn               = "powered_on"
	KeyOperationStarted        = "operation_started"
	KeyQRPresent               = "qr_present"
	KeyFormatValid             = "format_valid"
	KeyDiscriminatorLength     = "discriminator_length"
	KeySetupCodeLength         = "setup_code_length"
)

// Network handler output keys.
const (
	KeyPartitionActive = "partition_active"
	KeyFilterActive    = "filter_active"
	KeyFilterType      = "filter_type"
	KeyInterfaceDown   = "interface_down"
	KeyInterfaceUp     = "interface_up"
	KeyFlapCount       = "flap_count"
	KeyAddressChanged  = "address_changed"
	KeyNewAddress      = "new_address"
	KeyDisplayChecked  = "display_checked"
	KeyQRDisplayed     = "qr_displayed"
	KeyClockAdjusted   = "clock_adjusted"
	KeyOffsetMs        = "offset_ms"
)

// Utility handler output keys.
const (
	KeyComparisonResult = "comparison_result"
	KeyValuesEqual      = "values_equal"
	KeyAllEqual         = "all_equal"
	KeyAllDifferent     = "all_different"
	KeySkipped          = "skipped"
	KeyTimeRecorded     = "time_recorded"
	KeyTimestampMs      = "timestamp_ms"
	KeyWithinTolerance  = "within_tolerance"
	KeyWithinLimit      = "within_limit"
	KeyWithinBounds     = "within_bounds"
	KeyElapsedMs        = "elapsed_ms"
	KeyStatusMatches    = "status_matches"
	KeyPayloadMatches   = "payload_matches"
	KeyCorrelationValid = "correlation_valid"
	KeyStateReached     = "state_reached"
	KeyReportReceived   = "report_received"
	KeyReportData       = "report_data"
	KeyVersion          = "version"
)

// Trigger handler output keys.
const (
	KeyTriggerSent  = "trigger_sent"
	KeyEventTrigger = "event_trigger"
	KeySuccess      = "success"
)

// Common parameter keys -- used in step.Params maps across multiple handlers.
const (
	KeyEndpoint      = "endpoint"
	KeyFeature       = "feature"
	KeyServiceType   = "service_type"
	KeyTimeoutMs     = "timeout_ms"
	KeyCommissioning = "commissioning"
	KeyEvent         = "event"
)

// Device handler output keys (additional).
const (
	KeyFailsafeLimitSet = "failsafe_limit_set"
	KeyLimitWatts       = "limit_watts"
)

// ZoneConnectionStateKey returns the state key for a zone's connection.
func ZoneConnectionStateKey(zoneID string) string {
	return "zone_" + zoneID + "_connection"
}

// PASE timing analysis error types.
const (
	TimingErrorInvalidPubkey = "invalid_pubkey"
	TimingErrorWrongPassword = "wrong_password"
)

// PASE error code names (mapped from commissioning.ErrCode* constants).
const (
	PASEErrorSuccess            = "SUCCESS"
	PASEErrorAuthFailed         = "AUTHENTICATION_FAILED"
	PASEErrorVerificationFailed = "VERIFICATION_FAILED"
	PASEErrorCSRFailed          = "CSR_FAILED"
	PASEErrorCertInstallFailed  = "CERT_INSTALL_FAILED"
	PASEErrorDeviceBusy         = "DEVICE_BUSY"
	PASEErrorZoneTypeExists     = "ZONE_TYPE_EXISTS"
)

// Service type short aliases -- used in YAML params alongside discovery.ServiceType* mDNS constants.
const (
	ServiceAliasCommissionable = "commissionable"
	ServiceAliasOperational    = "operational"
	ServiceAliasPairingRequest = "pairing_request"
)

// Connection state values.
const (
	ConnectionStateOperational  = "OPERATIONAL"
	ConnectionStateClosed       = "CLOSED"
	ConnectionStateDisconnected = "DISCONNECTED"
	ConnectionStatePASEVerified = "PASE_VERIFIED"
)

// Commissioning state values (used by handleVerifyCommissioningState).
const (
	CommissioningStateAdvertising = "ADVERTISING"
	CommissioningStateConnected   = "CONNECTED"
	CommissioningStateCommissioned = "COMMISSIONED"
)

// Close reason values.
const (
	CloseReasonKeepaliveTimeout = "KEEPALIVE_TIMEOUT"
	CloseReasonNormal           = "NORMAL"
)

// Network filter values.
const (
	NetworkFilterBlockPongs = "block_pongs"
)

// Zone type string values -- protocol-defined zone types for multi-zone architecture.
const (
	ZoneTypeGrid  = "GRID"
	ZoneTypeLocal = "LOCAL"
	ZoneTypeTest  = "TEST"
)

// OperatingStateEnum values.
const (
	OperatingStateStandby = "STANDBY"
	OperatingStateRunning = "RUNNING"
	OperatingStateFault   = "FAULT"
)

// ControlStateEnum values.
const (
	ControlStateAutonomous = "AUTONOMOUS"
	ControlStateControlled = "CONTROLLED"
	ControlStateLimited    = "LIMITED"
	ControlStateFailsafe   = "FAILSAFE"
	ControlStateOverride   = "OVERRIDE"
)

// ProcessStateEnum values.
const (
	ProcessStateNone      = "NONE"
	ProcessStateAvailable = "AVAILABLE"
	ProcessStateScheduled = "SCHEDULED"
	ProcessStateRunning   = "RUNNING"
	ProcessStatePaused    = "PAUSED"
	ProcessStateCompleted = "COMPLETED"
	ProcessStateAborted   = "ABORTED"
)

// Error code values -- used as KeyErrorCode values in output maps.
const (
	ErrCodeTimeout               = "TIMEOUT"
	ErrCodeConnectionFailed      = "CONNECTION_FAILED"
	ErrCodeTLSError              = "TLS_ERROR"
	ErrCodeConnectionError       = "CONNECTION_ERROR"
	ErrCodeMaxConnsExceeded      = "MAX_CONNECTIONS_EXCEEDED"
	ErrCodeNoDevicesFound        = "NO_DEVICES_FOUND"
	ErrCodeAddrResolutionFailed  = "ADDRESS_RESOLUTION_FAILED"
	ErrCodeDiscriminatorMismatch = "DISCRIMINATOR_MISMATCH"
)

// PASE handler output keys.
const (
	KeyXSent     = "x_sent"
	KeyYReceived = "y_received"
)

// Capacity handler output keys.
const (
	KeyAllEstablished   = "all_established"
	KeyEstablishedCount = "established_count"
)

// Concurrency handler output keys.
const (
	KeyResponseCount = "response_count"
)

// Subscription extended output keys (additional).
const (
	KeySubscriptionIDPresent = "subscription_id_present"
	KeyPrimingIsPrimingFlag  = "priming_is_priming_flag"
)

// Save-as output keys -- echoed back so tests can assert the save happened.
const (
	KeySaveDelayAs        = "save_delay_as"
	KeySaveSubscriptionID = "save_subscription_id"
)

// Queue handler output keys.
const (
	KeyQueued             = "queued"
	KeyDisconnectOccurred = "disconnect_occurred"
)

// Device handler output keys (has_exposed_device).
const (
	KeyHasExposedDevice = "has_exposed_device"
)

// Parameter keys -- used in step.Params maps for specific handlers.
const (
	// General operation params.
	ParamCommand   = "command"
	ParamArgs      = "args"
	ParamParams    = "params"
	ParamPayload   = "payload"
	ParamRequests  = "requests"
	ParamValue     = "value"
	ParamAttribute = "attribute"
	ParamName      = "name"
	ParamType      = "type"
	ParamAsync     = "async"
	ParamGraceful  = "graceful"
	ParamBlock     = "block"
	ParamTimeout   = "timeout"

	// Zone/connection params.
	ParamZone                    = "zone"
	ParamZoneIDRef               = "zone_id_ref"
	ParamSubscriptionIDRef       = "subscription_id_ref"
	ParamConnection              = "connection"
	ParamTransitionToOperational  = "transition_to_operational"
	ParamFromPrecondition        = "_from_precondition"
	ParamDisconnectBeforeResponse = "disconnect_before_response"

	// PASE/identity params.
	ParamPassword     = "password"
	ParamInvalidPoint = "invalid_point"
	ParamXValue       = "x_value"
	ParamEnableKey    = "enable_key"

	// Keepalive/network params.
	ParamBlockPong  = "block_pong"
	ParamBlockPongs = "block_pongs"
	ParamSeq        = "seq"

	// Send raw params.
	ParamMessageType           = "message_type"
	ParamMessageID             = "message_id"
	ParamCBORMap               = "cbor_map"
	ParamCBORMapStringKeys     = "cbor_map_string_keys"
	ParamCBORBytesHex          = "cbor_bytes_hex"
	ParamBytesHex              = "bytes_hex"
	ParamData                  = "data"
	ParamRemainingBytes        = "remaining_bytes"
	ParamFollowedByBytes       = "followed_by_bytes"
	ParamFollowedByCBORPayload = "followed_by_cbor_payload"
	ParamPayloadSize           = "payload_size"
	ParamLengthOverride        = "length_override"
	ParamValidCBOR             = "valid_cbor"

	// Command queue / multiple params.
	ParamCommands         = "commands"
	ParamSaveDelayAs      = "save_delay_as"
	ParamSaveSubscriptionID = "save_subscription_id"
	ParamHasExposedDevice = "has_exposed_device"
	ParamDuration         = "duration"
	ParamFeatures         = "features"

	// Device state params.
	ParamSubAction      = "sub_action"
	ParamOperatingState = "operating_state"
	ParamTargetState    = "target_state"
	ParamControlState   = "control_state"
	ParamProcessState   = "process_state"
	ParamChanges        = "changes"
	ParamEndpoints      = "endpoints"
	ParamAttributes     = "attributes"
	ParamFaultMessage   = "fault_message"
	ParamExpectedState  = "expected_state"
	ParamSimulateNoResponse = "simulate_no_response"

	// Timing/comparison params.
	ParamLeft             = "left"
	ParamRight            = "right"
	ParamOperator         = "operator"
	ParamValues           = "values"
	ParamExpression       = "expression"
	ParamCondition        = "condition"
	ParamStartKey         = "start_key"
	ParamEndKey           = "end_key"
	ParamReference        = "reference"
	ParamMinDuration      = "min_duration"
	ParamMaxDuration      = "max_duration"
	ParamExpectedDuration = "expected_duration"
	ParamTolerance        = "tolerance"
	ParamExpectedStatus   = "expected_status"
	ParamExpectedFields   = "expected_fields"
	ParamRequestKey       = "request_key"
	ParamResponseKey      = "response_key"
	ParamContent          = "content"

	// Network params.
	ParamIntervalMs    = "interval_ms"
	ParamOffsetSeconds = "offset_seconds"
	ParamOffsetDays    = "offset_days"

	// Discovery params.
	ParamRetry          = "retry"
	ParamRequiredFields = "required_fields"
	ParamRequiredKeys   = "required_keys"
	ParamAdminToken     = "admin_token"
)

// Internal state keys (not output keys).
const (
	StateStartTime   = "start_time"
	StateEndTime     = "end_time"
	StatePrimingData          = "_priming_data"
	StatePrimingQueue         = "_priming_queue"
	StatePrimingAttrCount     = "_priming_attr_count"
	StateNotificationCounter  = "_notification_counter"
)

// Checker registration names -- used in runner's registerHandlers.
const (
	CheckerConnectionEstablished = "connection_established"
	CheckerResponseReceived      = "response_received"
	CheckerResponseDelayMsMin    = "response_delay_ms_min"
	CheckerResponseDelayMsMax    = "response_delay_ms_max"
	CheckerMaxDelayMs            = "max_delay_ms"
	CheckerMinDelayMs            = "min_delay_ms"
	CheckerMeanDifferenceMsMax   = "mean_difference_ms_max"
	CheckerValidityDaysMin       = "validity_days_min"
	CheckerBusyRetryAfterGT      = "busy_retry_after_gt"
)

// ============================================================================
// Action constants -- handler names registered with engine.RegisterHandler.
// Grouped by handler file for discoverability.
// ============================================================================

// Core actions (runner.go).
const (
	ActionConnect    = "connect"
	ActionDisconnect = "disconnect"
	ActionRead       = "read"
	ActionWrite      = "write"
	ActionSubscribe  = "subscribe"
	ActionInvoke     = "invoke"
	ActionWait       = "wait"
	ActionVerify     = "verify"
)

// PASE commissioning actions (pase.go).
const (
	ActionCommission         = "commission"
	ActionPASERequest        = "pase_request"
	ActionPASEReceiveResponse = "pase_receive_response"
	ActionPASEConfirm        = "pase_confirm"
	ActionPASEReceiveVerify  = "pase_receive_verify"
	ActionVerifySessionKey   = "verify_session_key"
)

// Discovery actions (discovery_handlers.go).
const (
	ActionBrowseMDNS              = "browse_mdns"
	ActionReadMDNSTXT             = "read_mdns_txt"
	ActionVerifyMDNSAdvertising   = "verify_mdns_advertising"
	ActionVerifyMDNSBrowsing      = "verify_mdns_browsing"
	ActionVerifyMDNSNotAdvertising = "verify_mdns_not_advertising"
	ActionVerifyMDNSNotBrowsing   = "verify_mdns_not_browsing"
	ActionGetQRPayload            = "get_qr_payload"
	ActionAnnouncePairingRequest  = "announce_pairing_request"
	ActionStopPairingRequest      = "stop_pairing_request"
	ActionStartDiscovery          = "start_discovery"
	ActionStopDiscovery           = "stop_discovery"
	ActionWaitForDevice           = "wait_for_device"
	ActionVerifyTXTRecords        = "verify_txt_records"
)

// Connection actions (connection_handlers.go).
const (
	ActionConnectAsController         = "connect_as_controller"
	ActionConnectAsZone               = "connect_as_zone"
	ActionReadAsZone                  = "read_as_zone"
	ActionInvokeAsZone                = "invoke_as_zone"
	ActionSubscribeAsZone             = "subscribe_as_zone"
	ActionWaitForNotificationAsZone   = "wait_for_notification_as_zone"
	ActionConnectWithTiming           = "connect_with_timing"
	ActionSendClose                   = "send_close"
	ActionSimultaneousClose           = "simultaneous_close"
	ActionWaitDisconnect              = "wait_disconnect"
	ActionCancelReconnect             = "cancel_reconnect"
	ActionMonitorReconnect            = "monitor_reconnect"
	ActionDisconnectAndMonitorBackoff = "disconnect_and_monitor_backoff"
	ActionPing                        = "ping"
	ActionPingMultiple                = "ping_multiple"
	ActionVerifyKeepalive             = "verify_keepalive"
	ActionSendRaw                     = "send_raw"
	ActionSendRawBytes                = "send_raw_bytes"
	ActionSendRawFrame                = "send_raw_frame"
	ActionSendTLSAlert                = "send_tls_alert"
	ActionQueueCommand                = "queue_command"
	ActionWaitForQueuedResult         = "wait_for_queued_result"
	ActionSendMultipleThenDisconnect  = "send_multiple_then_disconnect"
	ActionOpenConnections             = "open_connections"
	ActionReadConcurrent              = "read_concurrent"
	ActionInvokeWithDisconnect        = "invoke_with_disconnect"
	ActionSubscribeMultiple           = "subscribe_multiple"
	ActionSubscribeOrdered            = "subscribe_ordered"
	ActionUnsubscribe                 = "unsubscribe"
	ActionReceiveNotification         = "receive_notification"
	ActionReceiveNotifications        = "receive_notifications"
)

// Security actions (security_handlers.go).
const (
	ActionOpenCommissioningConnection = "open_commissioning_connection"
	ActionCloseConnection             = "close_connection"
	ActionFloodConnections            = "flood_connections"
	ActionCheckActiveConnections      = "check_active_connections"
	ActionCheckConnectionClosed       = "check_connection_closed"
	ActionCheckMDNSAdvertisement      = "check_mdns_advertisement"
	ActionConnectOperational          = "connect_operational"
	ActionEnterCommissioningMode      = "enter_commissioning_mode"
	ActionExitCommissioningMode       = "exit_commissioning_mode"
	ActionSendPing                    = "send_ping"
	ActionReconnectOperational        = "reconnect_operational"
	ActionPASERequestSlow             = "pase_request_slow"
	ActionContinueSlowExchange        = "continue_slow_exchange"
	ActionPASEAttempts                = "pase_attempts"
	ActionPASEAttemptTimed            = "pase_attempt_timed"
	ActionPASERequestInvalidPubkey    = "pase_request_invalid_pubkey"
	ActionPASERequestWrongPassword    = "pase_request_wrong_password"
	ActionMeasureErrorTiming          = "measure_error_timing"
	ActionCompareTimingDistributions  = "compare_timing_distributions"
	ActionFillConnections             = "fill_connections"
)

// Renewal actions (renewal_handlers.go).
const (
	ActionSendRenewalRequest       = "send_renewal_request"
	ActionReceiveRenewalCSR        = "receive_renewal_csr"
	ActionSendCertInstall          = "send_cert_install"
	ActionReceiveRenewalAck        = "receive_renewal_ack"
	ActionFullRenewalFlow          = "full_renewal_flow"
	ActionRecordSubscriptionState  = "record_subscription_state"
	ActionVerifySubscriptionActive = "verify_subscription_active"
	ActionVerifyConnectionState    = "verify_connection_state"
	ActionSetCertExpiry            = "set_cert_expiry"
	ActionWaitForNotification      = "wait_for_notification"
	ActionVerifyNotificationContent = "verify_notification_content"
	ActionSimulateCertExpiry       = "simulate_cert_expiry"
	ActionConnectExpectFailure     = "connect_expect_failure"
	ActionSetGracePeriod           = "set_grace_period"
	ActionSimulateTimeAdvance      = "simulate_time_advance"
	ActionCheckGracePeriodStatus   = "check_grace_period_status"
)

// Network actions (network_handlers.go).
const (
	ActionNetworkPartition = "network_partition"
	ActionNetworkFilter    = "network_filter"
	ActionInterfaceDown    = "interface_down"
	ActionInterfaceUp      = "interface_up"
	ActionInterfaceFlap    = "interface_flap"
	ActionChangeAddress    = "change_address"
	ActionCheckDisplay     = "check_display"
	ActionAdjustClock      = "adjust_clock"
)

// Device actions (device_handlers.go).
const (
	ActionDeviceLocalAction      = "device_local_action"
	ActionDeviceSetValue         = "device_set_value"
	ActionDeviceSetValuesRapid   = "device_set_values_rapid"
	ActionDeviceTrigger          = "device_trigger"
	ActionConfigureDevice        = "configure_device"
	ActionConfigureExposedDevice = "configure_exposed_device"
	ActionUpdateExposedAttribute = "update_exposed_attribute"
	ActionChangeState            = "change_state"
	ActionSetStateDetail         = "set_state_detail"
	ActionTriggerFault           = "trigger_fault"
	ActionClearFault             = "clear_fault"
	ActionQueryDeviceState       = "query_device_state"
	ActionVerifyDeviceState      = "verify_device_state"
	ActionSetConnected           = "set_connected"
	ActionSetDisconnected        = "set_disconnected"
	ActionSetFailsafeLimit       = "set_failsafe_limit"
	ActionMakeProcessAvailable   = "make_process_available"
	ActionStartOperation         = "start_operation"
	ActionEVConnect              = "ev_connect"
	ActionEVDisconnect           = "ev_disconnect"
	ActionEVRequestsCharge       = "ev_requests_charge"
	ActionPlugInCable            = "plug_in_cable"
	ActionUnplugCable            = "unplug_cable"
	ActionUserOverride           = "user_override"
	ActionFactoryReset           = "factory_reset"
	ActionPowerCycle             = "power_cycle"
	ActionPowerOnDevice          = "power_on_device"
	ActionReboot                 = "reboot"
	ActionRestart                = "restart"
)

// Controller actions (controller_handlers.go).
const (
	ActionControllerAction                = "controller_action"
	ActionCommissionWithAdmin             = "commission_with_admin"
	ActionGetControllerID                 = "get_controller_id"
	ActionVerifyControllerCert            = "verify_controller_cert"
	ActionVerifyControllerState           = "verify_controller_state"
	ActionSetCommissioningWindowDuration  = "set_commissioning_window_duration"
	ActionGetCommissioningWindowDuration  = "get_commissioning_window_duration"
	ActionRemoveDevice                    = "remove_device"
	ActionRenewCert                       = "renew_cert"
	ActionCheckRenewal                    = "check_renewal"
)

// Zone actions (zone_handlers.go).
const (
	ActionCreateZone                 = "create_zone"
	ActionSetLogicalZone             = "set_logical_zone"
	ActionAddZone                    = "add_zone"
	ActionDeleteZone                 = "delete_zone"
	ActionRemoveZone                 = "remove_zone"
	ActionGetZone                    = "get_zone"
	ActionHasZone                    = "has_zone"
	ActionListZones                  = "list_zones"
	ActionZoneCount                  = "zone_count"
	ActionGetZoneMetadata            = "get_zone_metadata"
	ActionGetZoneCAFingerprint       = "get_zone_ca_fingerprint"
	ActionVerifyZoneCA               = "verify_zone_ca"
	ActionVerifyZoneBinding          = "verify_zone_binding"
	ActionVerifyZoneIDDerivation     = "verify_zone_id_derivation"
	ActionHighestPriorityZone        = "highest_priority_zone"
	ActionHighestPriorityConnectedZone = "highest_priority_connected_zone"
	ActionDisconnectZone             = "disconnect_zone"
	ActionVerifyOtherZone            = "verify_other_zone"
	ActionVerifyBidirectionalActive  = "verify_bidirectional_active"
	ActionVerifyRestoreSequence      = "verify_restore_sequence"
	ActionVerifyTLSState             = "verify_tls_state"
)

// Cert actions (cert_handlers.go).
const (
	ActionVerifyCertificate        = "verify_certificate"
	ActionVerifyCertSubject        = "verify_cert_subject"
	ActionVerifyDeviceCert         = "verify_device_cert"
	ActionVerifyDeviceCertStore    = "verify_device_cert_store"
	ActionGetCertFingerprint       = "get_cert_fingerprint"
	ActionExtractCertDeviceID      = "extract_cert_device_id"
	ActionVerifyCommissioningState = "verify_commissioning_state"
	ActionResetPASESession         = "reset_pase_session"
	ActionSendPASEX                = "send_pase_x"
	ActionDeviceVerifyPeer         = "device_verify_peer"
	ActionReceiveCertRenewalAck    = "receive_cert_renewal_ack"
	ActionReceiveCertRenewalCSR    = "receive_cert_renewal_csr"
	ActionSendCertRenewalInstall   = "send_cert_renewal_install"
	ActionSendCertRenewalRequest   = "send_cert_renewal_request"
	ActionSetCertExpiryDays        = "set_cert_expiry_days"
)

// Utility actions (utility_handlers.go).
const (
	ActionCompare           = "compare"
	ActionCompareValues     = "compare_values"
	ActionEvaluate          = "evaluate"
	ActionConditionalRead   = "conditional_read"
	ActionRecordTime        = "record_time"
	ActionVerifyTiming      = "verify_timing"
	ActionCheckResponse     = "check_response"
	ActionVerifyCorrelation = "verify_correlation"
	ActionWaitForState      = "wait_for_state"
	ActionWaitNotification  = "wait_notification"
	ActionWaitReport        = "wait_report"
	ActionParseQR           = "parse_qr"
)

// Trigger actions (trigger_handlers.go).
const (
	ActionTriggerTestEvent = "trigger_test_event"
)
