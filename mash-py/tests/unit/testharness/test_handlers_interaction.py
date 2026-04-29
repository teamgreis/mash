"""Interaction-handler unit tests with a mock InteractionClient.

The wire-talking paths are exercised by the interop suite. Here we only
validate parameter coercion / error mapping by stubbing the underlying
InteractionClient via :func:`monkeypatch`.
"""

from __future__ import annotations

from typing import Any

import pytest

from mash_py.interaction.client import StatusError
from mash_py.testharness.handlers import interaction as handler_mod
from mash_py.testharness.loader import Step
from mash_py.testharness.runner import ExecutionState
from mash_py.wire import Status

pytestmark = pytest.mark.asyncio


class _StubClient:
    is_closed: bool = False

    def __init__(self) -> None:
        self.read_calls: list[tuple[int, int, list[int] | None]] = []
        self.write_calls: list[tuple[int, int, dict[int, Any]]] = []
        self.invoke_calls: list[tuple[int, int, int, dict[int, Any]]] = []

    async def read(self, ep: int, feat: int, ids: list[int] | None) -> dict[int, Any]:
        self.read_calls.append((ep, feat, ids))
        return {1: "vendor", 2: "product"}

    async def write(self, ep: int, feat: int, vals: dict[int, Any]) -> dict[int, Any]:
        self.write_calls.append((ep, feat, vals))
        return vals

    async def invoke(self, ep: int, feat: int, cmd: int, args: dict[int, Any]) -> Any:
        self.invoke_calls.append((ep, feat, cmd, args))
        return {99: "ok"}


@pytest.fixture
def stub_state(monkeypatch: pytest.MonkeyPatch) -> tuple[ExecutionState, _StubClient]:
    state = ExecutionState()
    stub = _StubClient()

    async def fake_ensure(s: ExecutionState) -> _StubClient:
        return stub

    monkeypatch.setattr(handler_mod, "_ensure_client", fake_ensure)
    return state, stub


async def test_read_returns_attribute_map(
    stub_state: tuple[ExecutionState, _StubClient],
) -> None:
    state, stub = stub_state
    out = await handler_mod.handle_read(
        Step(
            action="read",
            params={"endpoint": 0, "feature": "DeviceInfo"},
        ),
        state,
    )
    assert out["read_success"] is True
    assert out["attribute_count"] == 2
    # DeviceInfo resolves to feature 0x01.
    assert stub.read_calls == [(0, 0x01, None)]


async def test_read_attribute_id_int(
    stub_state: tuple[ExecutionState, _StubClient],
) -> None:
    state, stub = stub_state
    await handler_mod.handle_read(
        Step(
            action="read",
            params={"endpoint": 1, "feature": 2, "attribute": 5},
        ),
        state,
    )
    assert stub.read_calls == [(1, 2, [5])]


async def test_read_status_error_surfaces(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    state = ExecutionState()

    class Boom:
        is_closed = False

        async def read(self, *_: Any, **__: Any) -> Any:
            raise StatusError(status=Status.INVALID_ATTRIBUTE, message="missing")

    async def fake_ensure(_s: ExecutionState) -> Any:
        return Boom()

    monkeypatch.setattr(handler_mod, "_ensure_client", fake_ensure)

    out = await handler_mod.handle_read(Step(action="read"), state)
    assert out["read_success"] is False
    assert out["status"] == int(Status.INVALID_ATTRIBUTE)


async def test_write_passes_int_keyed_values(
    stub_state: tuple[ExecutionState, _StubClient],
) -> None:
    state, stub = stub_state
    out = await handler_mod.handle_write(
        Step(
            action="write",
            params={
                "endpoint": 0,
                "feature": 4,
                "values": {"3": 100, 4: 200},
            },
        ),
        state,
    )
    assert out["write_success"] is True
    assert stub.write_calls == [(0, 4, {3: 100, 4: 200})]


async def test_invoke_returns_value(
    stub_state: tuple[ExecutionState, _StubClient],
) -> None:
    state, stub = stub_state
    out = await handler_mod.handle_invoke(
        Step(
            action="invoke",
            params={"endpoint": 0, "feature": 5, "command": 7, "args": {1: "x"}},
        ),
        state,
    )
    assert out["invoke_success"] is True
    assert out["value"] == {99: "ok"}
    assert stub.invoke_calls == [(0, 5, 7, {1: "x"})]


async def test_no_operational_connection_returns_clear_error() -> None:
    state = ExecutionState()
    out = await handler_mod.handle_read(Step(action="read"), state)
    assert out["read_success"] is False
    assert "no operational" in out["error"]


async def test_subscribe_emits_full_output_set(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """handle_subscribe must mirror Go's output keys so YAML assertions work.

    The mash-go runner exposes subscribe_success / subscription_id_returned /
    subscription_id_saved / priming_received / priming_is_priming_flag, and
    YAML cases reference those names. The Python harness used to return only
    subscribe_success+subscription_id, which cascade-failed every subscribe
    expectation.
    """
    state = ExecutionState()

    class _SubClient:
        is_closed = False

        async def subscribe(
            self,
            ep: int,
            feat: int,
            *,
            handler: Any,
            min_interval_ms: int,
            max_interval_ms: int,
        ) -> Any:
            from mash_py.interaction.client import Subscription

            return Subscription(
                subscription_id=42,
                endpoint_id=ep,
                feature_id=feat,
                initial_values={1: 100, 2: "running"},
            )

    async def fake_ensure(_s: ExecutionState) -> Any:
        return _SubClient()

    monkeypatch.setattr(handler_mod, "_ensure_client", fake_ensure)

    out = await handler_mod.handle_subscribe(
        Step(action="subscribe", params={"endpoint": 1, "feature": 4}),
        state,
    )
    assert out["subscribe_success"] is True
    assert out["subscription_id"] == 42
    assert out["subscription_id_returned"] is True
    assert out["subscription_id_saved"] is True
    assert out["subscription_id_present"] is True
    assert out["priming_received"] is True
    assert out["priming_is_priming_flag"] is True
    assert out["priming_value"] == {1: 100, 2: "running"}
    # Priming data must also be stashed for save_priming_value to retrieve.
    assert state.custom["_priming_data"] == {1: 100, 2: "running"}


async def test_subscribe_status_error_keeps_id_flags_false(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    state = ExecutionState()

    class _BadClient:
        is_closed = False

        async def subscribe(self, *_: Any, **__: Any) -> Any:
            raise StatusError(status=Status.INVALID_ATTRIBUTE, message="nope")

    async def fake_ensure(_s: ExecutionState) -> Any:
        return _BadClient()

    monkeypatch.setattr(handler_mod, "_ensure_client", fake_ensure)

    out = await handler_mod.handle_subscribe(Step(action="subscribe"), state)
    assert out["subscribe_success"] is False
    assert out["subscription_id_returned"] is False
    assert out["subscription_id_saved"] is False
    assert out["priming_is_priming_flag"] is False
