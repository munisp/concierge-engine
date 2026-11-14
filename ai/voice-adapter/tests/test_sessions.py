"""Voice session state machine."""

import pytest

from voice_adapter.sessions import (CREATED, ENDED, FAILED, LIVE,
                                    IllegalTransition, SessionStore)


def make(store, tenant="t1", room="r1"):
    return store.create(tenant, "c1", "+971500000000", room, "conv1")


def test_happy_path_lifecycle():
    s = SessionStore()
    sess = make(s)
    assert sess.state == CREATED
    s.transition(sess, LIVE)
    assert sess.live_at is not None
    s.transition(sess, ENDED, "room finished")
    assert sess.ended_at is not None and sess.end_reason == "room finished"


def test_created_to_ended_is_no_show():
    s = SessionStore()
    sess = make(s)
    s.transition(sess, ENDED, "caller never joined")
    assert sess.state == ENDED


def test_terminal_states_refuse():
    s = SessionStore()
    sess = make(s)
    s.transition(sess, ENDED, "done")
    with pytest.raises(IllegalTransition):
        s.transition(sess, LIVE)


def test_created_to_failed():
    s = SessionStore()
    sess = make(s)
    s.transition(sess, FAILED, "media error")
    assert sess.state == FAILED
    with pytest.raises(IllegalTransition):
        s.transition(sess, ENDED, "too late")


def test_tenant_isolation():
    s = SessionStore()
    sess = make(s, tenant="t1")
    assert s.get_for_tenant("t1", sess.id) is sess
    assert s.get_for_tenant("t2", sess.id) is None  # cross-tenant blind


def test_active_filtering():
    s = SessionStore()
    a = make(s, tenant="t1", room="r1")
    b = make(s, tenant="t2", room="r2")
    s.transition(a, LIVE)
    s.transition(b, ENDED, "done")
    assert [x.id for x in s.active()] == [a.id]
    assert s.active(tenant_id="t1")[0].id == a.id
    assert s.active(tenant_id="t2") == []
