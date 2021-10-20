// SPDX-License-Identifier: MIT
//
// ▓▓▌ ▓▓ ▐▓▓ ▓▓▓▓▓▓▓▓▓▓▌▐▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓ ▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓ ▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▄
// ▓▓▓▓▓▓▓▓▓▓ ▓▓▓▓▓▓▓▓▓▓▌▐▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓ ▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓ ▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓
//   ▓▓▓▓▓▓    ▓▓▓▓▓▓▓▀    ▐▓▓▓▓▓▓    ▐▓▓▓▓▓   ▓▓▓▓▓▓     ▓▓▓▓▓   ▐▓▓▓▓▓▌   ▐▓▓▓▓▓▓
//   ▓▓▓▓▓▓▄▄▓▓▓▓▓▓▓▀      ▐▓▓▓▓▓▓▄▄▄▄         ▓▓▓▓▓▓▄▄▄▄         ▐▓▓▓▓▓▌   ▐▓▓▓▓▓▓
//   ▓▓▓▓▓▓▓▓▓▓▓▓▓▀        ▐▓▓▓▓▓▓▓▓▓▓         ▓▓▓▓▓▓▓▓▓▓         ▐▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓
//   ▓▓▓▓▓▓▀▀▓▓▓▓▓▓▄       ▐▓▓▓▓▓▓▀▀▀▀         ▓▓▓▓▓▓▀▀▀▀         ▐▓▓▓▓▓▓▓▓▓▓▓▓▓▓▀
//   ▓▓▓▓▓▓   ▀▓▓▓▓▓▓▄     ▐▓▓▓▓▓▓     ▓▓▓▓▓   ▓▓▓▓▓▓     ▓▓▓▓▓   ▐▓▓▓▓▓▌
// ▓▓▓▓▓▓▓▓▓▓ █▓▓▓▓▓▓▓▓▓ ▐▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓ ▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓  ▓▓▓▓▓▓▓▓▓▓
// ▓▓▓▓▓▓▓▓▓▓ ▓▓▓▓▓▓▓▓▓▓ ▐▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓ ▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓  ▓▓▓▓▓▓▓▓▓▓
//
//                           Trust math, not hardware.

pragma solidity ^0.8.6;

import "@openzeppelin/contracts/token/ERC20/IERC20.sol";
import "@openzeppelin/contracts/token/ERC20/utils/SafeERC20.sol";
import "./BLS.sol";
import "./Groups.sol";

library Relay {
    using SafeERC20 for IERC20;

    struct Request {
        // Request identifier.
        uint64 id;
        // Identifier of group responsible for signing.
        uint64 groupId;
        // Request start block.
        uint128 startBlock;
    }

    struct Data {
        // Total count of all requests.
        uint64 requestCount;
        // Previous entry value.
        bytes previousEntry;
        // Data of current request.
        Request currentRequest;
        // Address of the T token contract.
        IERC20 tToken;
        // Fee paid by the relay requester.
        uint256 relayRequestFee;
        // The number of blocks it takes for a group member to become
        // eligible to submit the relay entry.
        uint256 relayEntrySubmissionEligibilityDelay;
        // Hard timeout in blocks for a group to submit the relay entry.
        uint256 relayEntryHardTimeout;
    }

    // Size of a group in the threshold relay.
    uint256 public constant groupSize = 64;

    event RelayEntryRequested(
        uint256 indexed requestId,
        uint64 groupId,
        bytes previousEntry
    );
    event RelayEntrySubmitted(uint256 indexed requestId, bytes entry);

    /// @notice Creates a request to generate a new relay entry, which will
    ///         include a random number (by signing the previous entry's
    ///         random number).
    /// @param groupId Identifier of the group chosen to handle the request.
    function requestEntry(Data storage self, uint64 groupId) internal {
        require(
            !isRequestInProgress(self),
            "Another relay request in progress"
        );

        // slither-disable-next-line reentrancy-events
        self.tToken.safeTransferFrom(
            msg.sender,
            address(this),
            self.relayRequestFee
        );

        uint64 currentRequestId = ++self.requestCount;

        // TODO: Accepting and storing the whole Group object is not efficient
        //       as a lot of data is copied. Revisit once `Groups` library is
        //       ready.
        self.currentRequest = Request(
            currentRequestId,
            groupId,
            uint128(block.number)
        );

        emit RelayEntryRequested(currentRequestId, groupId, self.previousEntry);
    }

    /// @notice Creates a new relay entry.
    /// @param submitterIndex Index of the entry submitter.
    /// @param entry Group BLS signature over the previous entry.
    /// @param group Group data.
    /// @return punishedMembersIndexes Array of members indexes which should
    ///         be punished for not submitting the relay entry on their turn.
    /// @return slashingFactor Percentage of members stakes which should be
    ///         slashed as punishment for exceeding the soft timeout.
    function submitEntry(
        Data storage self,
        uint256 submitterIndex,
        bytes calldata entry,
        Groups.Group memory group
    )
        internal
        returns (
            uint256[] memory punishedMembersIndexes,
            uint256 slashingFactor
        )
    {
        require(isRequestInProgress(self), "No relay request in progress");
        // TODO: Add timeout reporting.
        require(!hasRequestTimedOut(self), "Relay request timed out");

        require(
            submitterIndex > 0 && submitterIndex <= groupSize,
            "Invalid submitter index"
        );
        require(
            group.members[submitterIndex - 1] == msg.sender,
            "Unexpected submitter index"
        );

        require(
            BLS.verify(group.groupPubKey, self.previousEntry, entry),
            "Invalid entry"
        );

        (
            uint256 firstEligibleIndex,
            uint256 lastEligibleIndex
        ) = getEligibilityRange(self, entry, groupSize);
        require(
            isEligible(
                self,
                submitterIndex,
                firstEligibleIndex,
                lastEligibleIndex,
                groupSize
            ),
            "Submitter is not eligible"
        );

        for (uint256 i = firstEligibleIndex; ; i++) {
            uint256 index = i > self.groupSize ? i - self.groupSize : i;

            if (index == submitterIndex) {
                break;
            }

            punishedMembersIndexes.push(index);

            if (index == lastEligibleIndex) {
                break;
            }
        }

        // If the soft timeout has been exceeded apply stake slashing.
        uint256 softTimeoutBlock = self.currentRequest.startBlock +
            (self.groupSize * self.relayEntrySubmissionEligibilityDelay);
        if (block.number > softTimeoutBlock) {
            uint256 submissionDelay = block.number - softTimeoutBlock;
            slashingFactor =
                (submissionDelay * 1e18) /
                self.relayEntryHardTimeout;
        }

        self.previousEntry = entry;
        delete self.currentRequest;

        emit RelayEntrySubmitted(self.requestCount, entry);

        return (punishedMembersIndexes, slashingFactor);
    }

    /// @notice Set relayRequestFee parameter.
    /// @param newRelayRequestFee New value of the parameter.
    function setRelayRequestFee(Data storage self, uint256 newRelayRequestFee)
        internal
    {
        require(!isRequestInProgress(self), "Relay request in progress");

        self.relayRequestFee = newRelayRequestFee;
    }

    /// @notice Set relayEntrySubmissionEligibilityDelay parameter.
    /// @param newRelayEntrySubmissionEligibilityDelay New value of the parameter.
    function setRelayEntrySubmissionEligibilityDelay(
        Data storage self,
        uint256 newRelayEntrySubmissionEligibilityDelay
    ) internal {
        require(!isRequestInProgress(self), "Relay request in progress");

        self
            .relayEntrySubmissionEligibilityDelay = newRelayEntrySubmissionEligibilityDelay;
    }

    /// @notice Set relayEntryHardTimeout parameter.
    /// @param newRelayEntryHardTimeout New value of the parameter.
    function setRelayEntryHardTimeout(
        Data storage self,
        uint256 newRelayEntryHardTimeout
    ) internal {
        require(!isRequestInProgress(self), "Relay request in progress");

        self.relayEntryHardTimeout = newRelayEntryHardTimeout;
    }

    /// @notice Returns whether a relay entry request is currently in progress.
    /// @return True if there is a request in progress. False otherwise.
    function isRequestInProgress(Data storage self)
        internal
        view
        returns (bool)
    {
        return self.currentRequest.id != 0;
    }

    /// @notice Returns whether the current relay request has timed out.
    /// @return True if the request timed out. False otherwise.
    function hasRequestTimedOut(Data storage self)
        internal
        view
        returns (bool)
    {
        uint256 relayEntryTimeout = (groupSize *
            self.relayEntrySubmissionEligibilityDelay) +
            self.relayEntryHardTimeout;

        return
            isRequestInProgress(self) &&
            block.number > self.currentRequest.startBlock + relayEntryTimeout;
    }

    /// @notice Determines the eligibility range for given relay entry basing on
    ///         current block number.
    /// @param _entry Entry value for which the eligibility range should be
    ///        determined.
    /// @param _groupSize Group size for which eligibility range should be determined.
    /// @return firstEligibleIndex Index of the first member which is eligible
    ///         to submit the relay entry.
    /// @return lastEligibleIndex Index of the last member which is eligible
    ///         to submit the relay entry.
    function getEligibilityRange(
        Data storage self,
        bytes calldata _entry,
        uint256 _groupSize
    )
        internal
        view
        returns (uint256 firstEligibleIndex, uint256 lastEligibleIndex)
    {
        // Modulo `groupSize` will give indexes in range <0, groupSize-1>
        // We count member indexes from `1` so we need to add `1` to the result.
        firstEligibleIndex = (uint256(keccak256(_entry)) % _groupSize) + 1;

        // Shift is computed by leveraging Solidity integer division which is
        // equivalent to floored division. That gives the desired result.
        // Shift value should be in range <0, groupSize-1> so we must cap
        // it explicitly.
        uint256 shift = (block.number - self.currentRequest.startBlock) /
            self.relayEntrySubmissionEligibilityDelay;
        shift = shift > _groupSize - 1 ? _groupSize - 1 : shift;

        // Last eligible index must be wrapped if their value is bigger than
        // the group size.
        lastEligibleIndex = firstEligibleIndex + shift;
        lastEligibleIndex = lastEligibleIndex > _groupSize
            ? lastEligibleIndex - _groupSize
            : lastEligibleIndex;

        return (firstEligibleIndex, lastEligibleIndex);
    }

    /// @notice Returns whether the given submitter index is eligible to submit
    ///         a relay entry within given eligibility range.
    /// @param _submitterIndex Index of the submitter whose eligibility is checked.
    /// @param _firstEligibleIndex First index of the given eligibility range.
    /// @param _lastEligibleIndex Last index of the given eligibility range.
    /// @param _groupSize Group size for which eligibility should be checked.
    /// @return True if eligible. False otherwise.
    function isEligible(
        /* solhint-disable-next-line no-unused-vars */
        Data storage self,
        uint256 _submitterIndex,
        uint256 _firstEligibleIndex,
        uint256 _lastEligibleIndex,
        uint256 _groupSize
    ) internal view returns (bool) {
        if (_firstEligibleIndex <= _lastEligibleIndex) {
            // First eligible index is equal or smaller than the last.
            // We just need to make sure the submitter index is in range
            // <firstEligibleIndex, lastEligibleIndex>.
            return
                _firstEligibleIndex <= _submitterIndex &&
                _submitterIndex <= _lastEligibleIndex;
        } else {
            // First eligible index is bigger than the last. We need to deal
            // with wrapped range and check whether the submitter index is
            // either in range <1, lastEligibleIndex> or
            // <firstEligibleIndex, groupSize>.
            return
                (1 <= _submitterIndex &&
                    _submitterIndex <= _lastEligibleIndex) ||
                (_firstEligibleIndex <= _submitterIndex &&
                    _submitterIndex <= _groupSize);
        }
    }
}
