Feature: SHOP-482

  @id:SHOP-482-05 @ticket:SHOP-482 @surface:api @auth:support-agent @intent-newline
  Scenario: A new order can be read back
    A new order is readable by its owner, with the status created.

    Given the fixture order.new as order
    When Read the order that the fixture made
    # step: read-order; GET /v1/orders/{id}
    Then the status is 200
    # ticket SHOP-482: the owner can read a new order
    And $.data.status equals "created"
    # ticket SHOP-482: a new order has the status created
