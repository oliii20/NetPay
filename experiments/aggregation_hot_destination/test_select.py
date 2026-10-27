import unittest
from select_destinations import select

class SelectionTest(unittest.TestCase):
    def test_coverage_and_ceiling(self):
        r=dict(shards=4,cross_shard=100,sha256='abc',transactions=150,
               ranking=[dict(shard=2,ctx=70),dict(shard=0,ctx=20),dict(shard=1,ctx=10),dict(shard=3,ctx=0)])
        a=select(r,25)
        self.assertEqual(a['destination_shards'],[2])
        self.assertEqual(a['coverage'],.7)
        self.assertEqual(select(r,26)['k'],2)
        self.assertEqual(select(r,100)['covered_ctx'],100)
        for v in (0,-1,101):
            with self.assertRaises(ValueError): select(r,v)

if __name__=='__main__':unittest.main()
