// SPDX-License-Identifier: MIT

pragma solidity ^0.8.6;

import "../RandomBeacon.sol";

// Stub contract used in tests
contract SortitionPoolStub is ISortitionPool {
    mapping(address => bool) public operators;
    mapping(address => bool) public eligibleOperators;

    event OperatorsRemoved(address[] operators);

    function joinPool(address operator) external override {
        operators[operator] = true;
    }

    function isOperatorInPool(address operator)
        external
        view
        override
        returns (bool)
    {
        return operators[operator];
    }

    // Helper function, it does not exist in the sortition pool
    function setOperatorEligibility(address operator, bool eligibility) public {
        eligibleOperators[operator] = eligibility;
    }

    function isOperatorEligible(address operator)
        public
        view
        override
        returns (bool)
    {
        return eligibleOperators[operator];
    }

    function removeOperators(address[] memory _operators) external override {
        for (uint256 i = 0; i < _operators.length; i++) {
            delete operators[_operators[i]];
            delete eligibleOperators[_operators[i]];
        }

        if (_operators.length > 0) {
            emit OperatorsRemoved(_operators);
        }
    }
}
