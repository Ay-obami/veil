// SPDX-License-Identifier: MIT
pragma solidity ^0.8.27;

import { ITeeExtensionRegistry } from "./interfaces/ITeeExtensionRegistry.sol";
import { ITeeMachineRegistry } from "./interfaces/ITeeMachineRegistry.sol";

interface IERC20 {
    function transferFrom(address from, address to, uint256 amount) external returns (bool);
    function transfer(address to, uint256 amount) external returns (bool);
}

/// @title VeilInstructionSender
/// @author Flare Foundation
/// @notice On-chain entry point for the orderbook extension. Acts as the vault for deposited
///         ERC20 tokens and sends on-chain instructions for deposits and withdrawals.
///         Orders, cancellations, state queries, and history exports go through direct
///         instructions (off-chain) and don't touch this contract.
///
/// DO NOT MODIFY: constructor, setExtensionId(), _getExtensionId()
contract VeilInstructionSender {
    // forge-lint: disable-next-line(unsafe-typecast)
    bytes32 public constant OP_TYPE_ORDERBOOK = bytes32("ORDERBOOK");
    // forge-lint: disable-next-line(unsafe-typecast)
    bytes32 public constant OP_COMMAND_DEPOSIT = bytes32("DEPOSIT");
    // forge-lint: disable-next-line(unsafe-typecast)
    bytes32 public constant OP_COMMAND_WITHDRAW = bytes32("WITHDRAW");

    ITeeExtensionRegistry public immutable TEE_EXTENSION_REGISTRY;
    ITeeMachineRegistry public immutable TEE_MACHINE_REGISTRY;

    /// @notice First public extension ID. The registry reserves IDs below this
    /// for system/reserved extensions; public extensions are assigned from here up.
    uint256 private constant FIRST_PUBLIC_EXTENSION_ID = 0x10000; // 65536

    uint256 private _extensionId;

    /// @notice TEE node address -- authorized signer for withdrawals.
    address public teeAddress;
    bool public teeAddressSet;

    /// @notice Admin addresses (set at deploy time).
    address[] public admins;

    /// @notice KYC allowlist for deposits. Only enforced when `kycEnabled` is true.
    mapping(address => bool) public allowed;

    /// @notice When true, only addresses in `allowed` may deposit. Defaults to false
    ///         (open access). Admins toggle via `setKycEnabled`.
    bool public kycEnabled;

    /// @notice Replay protection for withdrawals.
    mapping(bytes32 => bool) public usedWithdrawalIds;

    modifier onlyAdmin() {
        _checkAdmin();
        _;
    }

    function _checkAdmin() internal view {
        bool isAdmin = false;
        for (uint256 i = 0; i < admins.length; i++) {
            if (admins[i] == msg.sender) { isAdmin = true; break; }
        }
        require(isAdmin, "not admin");
    }

    constructor(
        ITeeExtensionRegistry _teeExtensionRegistry,
        ITeeMachineRegistry _teeMachineRegistry,
        address[] memory _admins
    ) {
        require(address(_teeExtensionRegistry) != address(0), "TeeExtensionRegistry cannot be zero address");
        require(address(_teeMachineRegistry) != address(0), "TeeMachineRegistry cannot be zero address");
        require(address(_teeExtensionRegistry).code.length > 0, "TeeExtensionRegistry has no code");
        require(address(_teeMachineRegistry).code.length > 0, "TeeMachineRegistry has no code");
        TEE_EXTENSION_REGISTRY = _teeExtensionRegistry;
        TEE_MACHINE_REGISTRY = _teeMachineRegistry;
        admins = _admins;
        for (uint256 i = 0; i < _admins.length; i++) {
            allowed[_admins[i]] = true;
        }
    }

    // --- Setup ---

    /// @notice Finds and sets this contract's extension id. Can only be set once.
    /// DO NOT MODIFY this function.
    function setExtensionId() external {
        require(_extensionId == 0, "Extension ID already set.");

        uint256 c = TEE_EXTENSION_REGISTRY.nextPublicExtensionId();
        for (uint256 i = FIRST_PUBLIC_EXTENSION_ID; i < c; ++i) {
            if (TEE_EXTENSION_REGISTRY.getTeeExtensionInstructionsSender(i) == address(this)) {
                _extensionId = i;
                return;
            }
        }
        revert("Extension ID not found.");
    }

    /// @notice Set the TEE node address (authorized withdrawal signer). Called once after TEE starts.
    function setTeeAddress(address _teeAddress) external onlyAdmin {
        // TODO: Perhaps require a TEE AVAILABILITY CHECK here, not just an admin set.
        require(!teeAddressSet, "TEE address already set");
        require(_teeAddress != address(0), "zero address");
        teeAddress = _teeAddress;
        teeAddressSet = true;
    }

    // --- Admin ---

    function allowUser(address user) external onlyAdmin {
        allowed[user] = true;
    }

    function removeUser(address user) external onlyAdmin {
        allowed[user] = false;
    }

    /// @notice Toggle KYC gating on deposits. When false (default), anyone may deposit.
    function setKycEnabled(bool enabled) external onlyAdmin {
        kycEnabled = enabled;
    }

    // --- Deposit (KYC-gated, on-chain instruction) ---

    /// @notice Deposit ERC20 tokens. Transfers tokens to this contract (vault) and sends
    ///         a DEPOSIT instruction to the TEE with the sender's address, token, and amount.
    /// @param token The ERC20 token address.
    /// @param amount The amount to deposit (in smallest units).
    function deposit(address token, uint256 amount) external payable {
        require(!kycEnabled || allowed[msg.sender], "not allowed to deposit");
        require(amount > 0, "zero amount");

        require(IERC20(token).transferFrom(msg.sender, address(this), amount), "transferFrom failed");

        // ABI-encode sender address into the message so the TEE can identify the depositor.
        bytes memory message = abi.encode(msg.sender, token, amount);
        _sendInstruction(OP_COMMAND_DEPOSIT, message);
    }

    // --- Withdraw ---

    /// @notice Request a withdrawal. The TEE will return signed authorization parameters.
    /// @param token The ERC20 token address.
    /// @param amount The amount to withdraw.
    /// @param to The destination address for the funds.
    function withdraw(address token, uint256 amount, address to) external payable {
        bytes memory message = abi.encode(msg.sender, token, amount, to);
        _sendInstruction(OP_COMMAND_WITHDRAW, message);
    }

    /// @notice Execute a TEE-authorized withdrawal. Anyone can call with valid signed parameters.
    /// @param token ERC20 token address.
    /// @param amount Amount to withdraw.
    /// @param to Destination address.
    /// @param withdrawalId Unique ID (instruction ID) for replay protection.
    /// @param signature TEE node's signature over keccak256(abi.encodePacked(token, amount, to, withdrawalId)).
    function executeWithdrawal(
        address token,
        uint256 amount,
        address to,
        bytes32 withdrawalId,
        bytes calldata signature
    ) external {
        require(teeAddressSet, "TEE not configured");
        require(!usedWithdrawalIds[withdrawalId], "withdrawal already executed");

        // Verify TEE signature.
        bytes32 hash = keccak256(abi.encodePacked(token, amount, to, withdrawalId));
        address signer = _recoverSigner(hash, signature);
        require(signer != address(0) && signer == teeAddress, "invalid TEE signature");

        usedWithdrawalIds[withdrawalId] = true;
        require(IERC20(token).transfer(to, amount), "transfer failed");
    }

    // --- Internal ---

    function _sendInstruction(bytes32 opCommand, bytes memory message) internal {
        address[] memory teeIds = TEE_MACHINE_REGISTRY.getRandomTeeIds(_getExtensionId(), 1);
        address[] memory cosigners = new address[](0);

        ITeeExtensionRegistry.TeeInstructionParams memory params = ITeeExtensionRegistry
            .TeeInstructionParams({
                opType: OP_TYPE_ORDERBOOK,
                opCommand: opCommand,
                message: message,
                cosigners: cosigners,
                cosignersThreshold: 0,
                claimBackAddress: msg.sender
            });

        TEE_EXTENSION_REGISTRY.sendInstructions{value: msg.value}(teeIds, params);
    }

    function _getExtensionId() internal view returns (uint256) {
        require(_extensionId != 0, "Extension ID is not set.");
        return _extensionId;
    }

    function _recoverSigner(bytes32 hash, bytes memory signature) internal pure returns (address) {
        // The TEE sign server, given `message`, signs EIP-191-prefixed keccak256(message).
        // The extension sends abi.encodePacked(token, amount, to, withdrawalId) as the message,
        // so keccak256(message) == `hash` here. The digest actually signed is therefore
        //   keccak256("\x19Ethereum Signed Message:\n32" || hash)
        // which is what we must recover against.
        bytes32 ethHash = keccak256(abi.encodePacked("\x19Ethereum Signed Message:\n32", hash));
        require(signature.length == 65, "invalid signature length");

        bytes32 r;
        bytes32 s;
        uint8 v;
        assembly {
            r := mload(add(signature, 32))
            s := mload(add(signature, 64))
            v := byte(0, mload(add(signature, 96)))
        }

        if (v < 27) {
            v += 27;
        }

        return ecrecover(ethHash, v, r, s);
    }
}
