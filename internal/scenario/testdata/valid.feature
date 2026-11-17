Feature: SHOP-482

  @id:SHOP-482-01 @ticket:SHOP-482 @surface:api @auth:support-agent @intent-newline
  Scenario: Refund above the order total is rejected
    Before: a refund larger than the order total was accepted. Now: the API rejects it with 422 and leaves the order unchanged.

    Given Create a paid order for 100000
    # step: create-order; POST /v1/orders
    When Request a refund of 150000 for that order
    # step: refund-too-much; POST /v1/orders/{id}/refunds
    Then the status is 422
    # ticket SHOP-482: a refund above the order total must be rejected
    And $.error.code equals "REFUND_EXCEEDS_TOTAL"
    # diff
    When Read the order again
    # step: order-unchanged; GET /v1/orders/{id}
    Then $.data.refunded_total equals 0
    # ticket SHOP-482: the order must stay unchanged
